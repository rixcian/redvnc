#ifndef SCREENCAPTURE_DARWIN_H
#define SCREENCAPTURE_DARWIN_H

#include <stddef.h>

// sckit_ctx_t holds cached ScreenCaptureKit state that is expensive to
// recreate on every frame. It supports two capture modes:
//
//   Phase A (screenshot): SCScreenshotManager one-shot captures. Uses a
//   cached display reference, CGColorSpaceRef, and persistent pixel buffer to
//   avoid per-frame allocations.
//
//   Phase B (stream): SCStream push-based streaming. The system calls a
//   delegate whenever a new frame is available; frames arrive as CVPixelBuffer
//   which already contains raw BGRA data — no CGContextDrawImage needed.
//   Use sckit_stream_start() to activate this mode; sckit_capture() then
//   waits for the next pushed frame with a short timeout.
//
// Allocate with sckit_init(); free with sckit_destroy().
typedef struct sckit_ctx {
    // Common fields (Phase A + B)
    void           *display;        // SCDisplay* (retained)
    void           *colorSpace;     // CGColorSpaceRef (retained, Phase A only)
    void           *pixelBuf;       // malloc'd BGRA buffer (width*height*4 bytes)
    int             width;
    int             height;
    int             stride;         // width * 4

    // Phase B stream fields (NULL when not streaming)
    void           *stream;         // SCStream* (retained)
    void           *streamOutput;   // SCKitStreamOutput* (retained)
    void           *frameSem;       // dispatch_semaphore_t — signals new frame
    void           *frameLock;      // dispatch_semaphore_t — guards pixelBuf writes (binary)
} sckit_ctx_t;

// sckit_init queries the main display once, caches its reference and a
// CGColorSpaceRef, and pre-allocates the persistent pixel buffer.
// Returns NULL on failure (permission denied, no display found, etc.).
sckit_ctx_t* sckit_init(void);

// sckit_destroy stops any running stream, releases all resources held by ctx.
void sckit_destroy(sckit_ctx_t *ctx);

// sckit_capture captures the main display into ctx->pixelBuf (BGRA format).
// In Phase A mode (stream not started) it uses SCScreenshotManager.
// In Phase B mode (after sckit_stream_start) it waits for the next pushed frame.
// Returns 0 on success. The pixel data is valid until the next sckit_capture
// call or sckit_destroy; the caller must not free it.
int sckit_capture(sckit_ctx_t *ctx);

// sckit_stream_start starts a persistent SCStream that pushes frames into
// ctx->pixelBuf via a CVPixelBuffer delegate. After this call, sckit_capture
// switches to pull-from-stream mode: it waits on frameSem rather than issuing
// a new SCScreenshotManager request each time.
// maxFPS controls the stream's minimumFrameInterval (0 = system default ~60).
// Returns 0 on success.
int sckit_stream_start(sckit_ctx_t *ctx, int maxFPS);

// sckit_stream_stop stops the SCStream and frees stream resources.
void sckit_stream_stop(sckit_ctx_t *ctx);

#endif
