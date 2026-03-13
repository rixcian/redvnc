//go:build darwin

#include "screencapture_darwin.h"
#import <ScreenCaptureKit/ScreenCaptureKit.h>
#import <CoreGraphics/CoreGraphics.h>

// Renders a CGImage into a caller-allocated BGRA buffer.
static int renderCGImageToBGRA(CGImageRef image, void *buf, int w, int h, int stride) {
    CGColorSpaceRef cs = CGColorSpaceCreateDeviceRGB();
    CGContextRef ctx = CGBitmapContextCreate(
        buf, (size_t)w, (size_t)h, 8, (size_t)stride,
        cs,
        kCGImageAlphaPremultipliedFirst | kCGBitmapByteOrder32Little // BGRA
    );
    CGColorSpaceRelease(cs);
    if (!ctx) {
        return -1;
    }
    CGContextDrawImage(ctx, CGRectMake(0, 0, w, h), image);
    CGContextRelease(ctx);
    return 0;
}

int sckit_get_display_size(int *outWidth, int *outHeight) {
    __block int resultWidth = 0;
    __block int resultHeight = 0;
    __block int resultCode = -1;

    dispatch_semaphore_t sem = dispatch_semaphore_create(0);

    [SCShareableContent getShareableContentWithCompletionHandler:^(SCShareableContent *content, NSError *error) {
        if (error || !content || content.displays.count == 0) {
            dispatch_semaphore_signal(sem);
            return;
        }

        SCDisplay *display = content.displays[0];
        resultWidth = (int)display.width;
        resultHeight = (int)display.height;
        resultCode = 0;
        dispatch_semaphore_signal(sem);
    }];

    dispatch_semaphore_wait(sem, dispatch_time(DISPATCH_TIME_NOW, 5 * NSEC_PER_SEC));

    *outWidth = resultWidth;
    *outHeight = resultHeight;
    return resultCode;
}

int sckit_capture_screen(void **outBuf, int *outWidth, int *outHeight, int *outStride) {
    __block void *resultBuf = NULL;
    __block int resultWidth = 0;
    __block int resultHeight = 0;
    __block int resultStride = 0;
    __block int resultCode = -1;

    dispatch_semaphore_t sem = dispatch_semaphore_create(0);

    [SCShareableContent getShareableContentWithCompletionHandler:^(SCShareableContent *content, NSError *error) {
        if (error || !content || content.displays.count == 0) {
            resultCode = -1;
            dispatch_semaphore_signal(sem);
            return;
        }

        SCDisplay *display = content.displays[0];
        SCContentFilter *filter = [[SCContentFilter alloc] initWithDisplay:display excludingWindows:@[]];

        SCStreamConfiguration *config = [[SCStreamConfiguration alloc] init];
        config.width = display.width;
        config.height = display.height;
        config.pixelFormat = kCVPixelFormatType_32BGRA;
        config.showsCursor = YES;

        [SCScreenshotManager captureImageWithFilter:filter
                                      configuration:config
                                  completionHandler:^(CGImageRef image, NSError *capError) {
            if (capError || !image) {
                resultCode = -2;
                dispatch_semaphore_signal(sem);
                return;
            }

            int w = (int)CGImageGetWidth(image);
            int h = (int)CGImageGetHeight(image);
            int stride = w * 4;

            void *buf = malloc((size_t)h * (size_t)stride);
            if (!buf) {
                resultCode = -3;
                dispatch_semaphore_signal(sem);
                return;
            }

            if (renderCGImageToBGRA(image, buf, w, h, stride) != 0) {
                free(buf);
                resultCode = -4;
                dispatch_semaphore_signal(sem);
                return;
            }

            resultBuf = buf;
            resultWidth = w;
            resultHeight = h;
            resultStride = stride;
            resultCode = 0;
            dispatch_semaphore_signal(sem);
        }];
    }];

    // Wait up to 5 seconds for the async capture to complete.
    dispatch_semaphore_wait(sem, dispatch_time(DISPATCH_TIME_NOW, 5 * NSEC_PER_SEC));

    *outBuf = resultBuf;
    *outWidth = resultWidth;
    *outHeight = resultHeight;
    *outStride = resultStride;
    return resultCode;
}
