//go:build darwin

#include "screencapture_darwin.h"
#import <ScreenCaptureKit/ScreenCaptureKit.h>
#import <CoreGraphics/CoreGraphics.h>
#import <CoreVideo/CoreVideo.h>
#import <Foundation/Foundation.h>
#include <stdlib.h>
#include <string.h>

// ---------------------------------------------------------------------------
// SCKitStreamOutput — SCStreamOutput delegate for Phase B streaming mode.
// Receives CVPixelBuffer frames from SCStream and copies them into ctx->pixelBuf.
// ---------------------------------------------------------------------------
@interface SCKitStreamOutput : NSObject <SCStreamOutput>
@property (nonatomic, assign) sckit_ctx_t *ctx;
@end

@implementation SCKitStreamOutput

- (void)stream:(SCStream *)stream
    didOutputSampleBuffer:(CMSampleBufferRef)sampleBuffer
    ofType:(SCStreamOutputType)type {

    if (type != SCStreamOutputTypeScreen) return;

    sckit_ctx_t *ctx = self.ctx;
    if (!ctx || !ctx->pixelBuf) return;

    CVImageBufferRef imageBuffer = CMSampleBufferGetImageBuffer(sampleBuffer);
    if (!imageBuffer) return;

    // Lock the pixel buffer for CPU read access.
    CVPixelBufferLockBaseAddress(imageBuffer, kCVPixelBufferLock_ReadOnly);

    void   *src    = CVPixelBufferGetBaseAddress(imageBuffer);
    size_t  srcRow = CVPixelBufferGetBytesPerRow(imageBuffer);
    int     w      = ctx->width;
    int     h      = ctx->height;
    int     dstRow = ctx->stride; // width * 4 (tight)

    // Acquire the write lock (binary semaphore) so we don't race with a
    // concurrent sckit_capture() read.
    dispatch_semaphore_wait((dispatch_semaphore_t)ctx->frameLock, DISPATCH_TIME_FOREVER);

    if ((int)srcRow == dstRow) {
        memcpy(ctx->pixelBuf, src, (size_t)dstRow * (size_t)h);
    } else {
        for (int row = 0; row < h; row++) {
            memcpy((char *)ctx->pixelBuf + row * dstRow,
                   (char *)src + row * srcRow, (size_t)dstRow);
        }
    }

    dispatch_semaphore_signal((dispatch_semaphore_t)ctx->frameLock);

    CVPixelBufferUnlockBaseAddress(imageBuffer, kCVPixelBufferLock_ReadOnly);

    // Signal that a new frame is ready for the consumer.
    // Non-blocking: if the consumer hasn't read the previous signal yet, skip.
    dispatch_semaphore_signal((dispatch_semaphore_t)ctx->frameSem);
}

- (void)stream:(SCStream *)stream didStopWithError:(NSError *)error {
    // Stream stopped unexpectedly; signal frameSem so any blocked
    // sckit_capture() call unblocks and returns an error.
    if (self.ctx && self.ctx->frameSem) {
        dispatch_semaphore_signal((dispatch_semaphore_t)self.ctx->frameSem);
    }
}

@end

// ---------------------------------------------------------------------------
// Phase A helper
// ---------------------------------------------------------------------------

// renderCGImageToBGRA renders a CGImageRef into a caller-provided BGRA buffer
// using the supplied (pre-created) color space to avoid recreating it per frame.
static int renderCGImageToBGRA(CGImageRef image, void *buf, int w, int h, int stride, CGColorSpaceRef cs) {
    CGContextRef ctx = CGBitmapContextCreate(
        buf, (size_t)w, (size_t)h, 8, (size_t)stride,
        cs,
        kCGImageAlphaPremultipliedFirst | kCGBitmapByteOrder32Little // BGRA
    );
    if (!ctx) {
        return -1;
    }
    CGContextDrawImage(ctx, CGRectMake(0, 0, w, h), image);
    CGContextRelease(ctx);
    return 0;
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

sckit_ctx_t* sckit_init(void) {
    __block SCDisplay *foundDisplay = nil;
    __block int resultCode = -1;

    dispatch_semaphore_t sem = dispatch_semaphore_create(0);

    [SCShareableContent getShareableContentWithCompletionHandler:^(SCShareableContent *content, NSError *error) {
        if (!error && content && content.displays.count > 0) {
            foundDisplay = content.displays[0];
            resultCode = 0;
        }
        dispatch_semaphore_signal(sem);
    }];

    dispatch_semaphore_wait(sem, dispatch_time(DISPATCH_TIME_NOW, 5 * NSEC_PER_SEC));

    if (resultCode != 0 || !foundDisplay) {
        return NULL;
    }

    sckit_ctx_t *ctx = (sckit_ctx_t *)calloc(1, sizeof(sckit_ctx_t));
    if (!ctx) {
        return NULL;
    }

    ctx->width  = (int)foundDisplay.width;
    ctx->height = (int)foundDisplay.height;
    ctx->stride = ctx->width * 4;

    // Retain the display reference for reuse across captures.
    ctx->display    = (void *)CFBridgingRetain(foundDisplay);
    // Create the color space once; reused in every renderCGImageToBGRA call.
    ctx->colorSpace = (void *)CGColorSpaceCreateDeviceRGB();

    ctx->pixelBuf = malloc((size_t)ctx->stride * (size_t)ctx->height);
    if (!ctx->pixelBuf) {
        CFRelease(ctx->display);
        CGColorSpaceRelease((CGColorSpaceRef)ctx->colorSpace);
        free(ctx);
        return NULL;
    }

    return ctx;
}

void sckit_destroy(sckit_ctx_t *ctx) {
    if (!ctx) return;
    sckit_stream_stop(ctx);
    if (ctx->display)    { CFRelease(ctx->display); }
    if (ctx->colorSpace) { CGColorSpaceRelease((CGColorSpaceRef)ctx->colorSpace); }
    if (ctx->pixelBuf)   { free(ctx->pixelBuf); }
    free(ctx);
}

int sckit_capture(sckit_ctx_t *ctx) {
    if (!ctx || !ctx->display || !ctx->pixelBuf) return -1;

    // Phase B: stream is running — wait for the next pushed frame.
    if (ctx->stream) {
        // Wait up to 200ms for the next frame from SCStream.
        long rc = dispatch_semaphore_wait(
            (dispatch_semaphore_t)ctx->frameSem,
            dispatch_time(DISPATCH_TIME_NOW, 200 * NSEC_PER_MSEC));
        // rc == 0 means a frame arrived; non-zero means timeout.
        // Either way we return whatever is in pixelBuf (may be previous frame on timeout).
        (void)rc;
        return 0;
    }

    // Phase A: one-shot screenshot via SCScreenshotManager.
    __block int resultCode = -1;

    SCDisplay *display = (__bridge SCDisplay *)ctx->display;
    SCContentFilter *filter = [[SCContentFilter alloc] initWithDisplay:display excludingWindows:@[]];

    SCStreamConfiguration *config = [[SCStreamConfiguration alloc] init];
    config.width       = ctx->width;
    config.height      = ctx->height;
    config.pixelFormat = kCVPixelFormatType_32BGRA;
    config.showsCursor = YES;

    CGColorSpaceRef cs     = (CGColorSpaceRef)ctx->colorSpace;
    void           *buf    = ctx->pixelBuf;
    int             w      = ctx->width;
    int             h      = ctx->height;
    int             stride = ctx->stride;

    dispatch_semaphore_t sem = dispatch_semaphore_create(0);

    [SCScreenshotManager captureImageWithFilter:filter
                                  configuration:config
                              completionHandler:^(CGImageRef image, NSError *capError) {
        if (!capError && image) {
            resultCode = renderCGImageToBGRA(image, buf, w, h, stride, cs) == 0 ? 0 : -2;
        } else {
            resultCode = -1;
        }
        dispatch_semaphore_signal(sem);
    }];

    // Wait up to 2 seconds; normal captures complete in 10–20ms.
    dispatch_semaphore_wait(sem, dispatch_time(DISPATCH_TIME_NOW, 2 * NSEC_PER_SEC));
    return resultCode;
}

int sckit_stream_start(sckit_ctx_t *ctx, int maxFPS) {
    if (!ctx || !ctx->display) return -1;
    if (ctx->stream) return 0; // already streaming

    // Create synchronisation primitives.
    // frameSem starts at 0 (no frame yet); frameLock starts at 1 (unlocked).
    ctx->frameSem  = (void *)dispatch_semaphore_create(0);
    ctx->frameLock = (void *)dispatch_semaphore_create(1);

    SCDisplay *display = (__bridge SCDisplay *)ctx->display;
    SCContentFilter *filter = [[SCContentFilter alloc] initWithDisplay:display excludingWindows:@[]];

    SCStreamConfiguration *config = [[SCStreamConfiguration alloc] init];
    config.width       = ctx->width;
    config.height      = ctx->height;
    config.pixelFormat = kCVPixelFormatType_32BGRA;
    config.showsCursor = YES;
    if (maxFPS > 0) {
        // minimumFrameInterval controls the max push rate (1/maxFPS seconds).
        config.minimumFrameInterval = CMTimeMake(1, maxFPS);
    }

    SCKitStreamOutput *output = [[SCKitStreamOutput alloc] init];
    output.ctx = ctx;
    ctx->streamOutput = (void *)CFBridgingRetain(output);

    SCStream *stream = [[SCStream alloc] initWithFilter:filter
                                          configuration:config
                                               delegate:nil];

    NSError *addErr = nil;
    BOOL ok = [stream addStreamOutput:output
                                 type:SCStreamOutputTypeScreen
                   sampleHandlerQueue:dispatch_get_global_queue(DISPATCH_QUEUE_PRIORITY_HIGH, 0)
                                error:&addErr];
    if (!ok || addErr) {
        CFRelease(ctx->streamOutput);
        ctx->streamOutput = NULL;
        return -2;
    }

    __block int startResult = -1;
    dispatch_semaphore_t startSem = dispatch_semaphore_create(0);

    [stream startCaptureWithCompletionHandler:^(NSError *startErr) {
        startResult = startErr ? -3 : 0;
        dispatch_semaphore_signal(startSem);
    }];

    dispatch_semaphore_wait(startSem, dispatch_time(DISPATCH_TIME_NOW, 5 * NSEC_PER_SEC));

    if (startResult != 0) {
        CFRelease(ctx->streamOutput);
        ctx->streamOutput = NULL;
        return startResult;
    }

    ctx->stream = (void *)CFBridgingRetain(stream);
    return 0;
}

void sckit_stream_stop(sckit_ctx_t *ctx) {
    if (!ctx || !ctx->stream) return;

    SCStream *stream = (__bridge SCStream *)ctx->stream;
    [stream stopCaptureWithCompletionHandler:^(NSError *err) {
        (void)err;
    }];

    CFRelease(ctx->stream);
    ctx->stream = NULL;

    if (ctx->streamOutput) {
        CFRelease(ctx->streamOutput);
        ctx->streamOutput = NULL;
    }
    // frameSem and frameLock are ARC/dispatch objects — released automatically
    // when the last reference goes away. NULL them out for safety.
    ctx->frameSem  = NULL;
    ctx->frameLock = NULL;
}
