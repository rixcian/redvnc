#ifndef SCREENCAPTURE_DARWIN_H
#define SCREENCAPTURE_DARWIN_H

// sckit_get_display_size retrieves the main display dimensions.
// Returns 0 on success.
int sckit_get_display_size(int *outWidth, int *outHeight);

// sckit_capture_screen captures the main display as BGRA pixel data.
// The caller must free *outBuf with free().
// Returns 0 on success.
int sckit_capture_screen(void **outBuf, int *outWidth, int *outHeight, int *outStride);

#endif
