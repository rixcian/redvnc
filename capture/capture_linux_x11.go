//go:build linux

package capture

/*
#cgo LDFLAGS: -lX11 -lXext
#include <X11/Xlib.h>
#include <X11/Xutil.h>
#include <X11/extensions/XShm.h>
#include <sys/ipc.h>
#include <sys/shm.h>
#include <stdlib.h>
#include <string.h>

typedef struct {
	Display *dpy;
	Window   root;
	int      width;
	int      height;
	int      depth;
	int      useShm;

	// XShm fields
	XShmSegmentInfo shminfo;
	XImage         *shmImage;

	// Fallback XGetImage buffer
	XImage *fallbackImage;
} x11_capture_t;

static x11_capture_t* x11_capture_init(int *out_w, int *out_h) {
	Display *dpy = XOpenDisplay(NULL);
	if (!dpy) return NULL;

	x11_capture_t *ctx = (x11_capture_t*)calloc(1, sizeof(x11_capture_t));
	ctx->dpy = dpy;
	ctx->root = DefaultRootWindow(dpy);

	XWindowAttributes attrs;
	XGetWindowAttributes(dpy, ctx->root, &attrs);
	ctx->width = attrs.width;
	ctx->height = attrs.height;
	ctx->depth = attrs.depth;

	*out_w = ctx->width;
	*out_h = ctx->height;

	// Try XShm
	if (XShmQueryExtension(dpy)) {
		ctx->shmImage = XShmCreateImage(dpy,
			DefaultVisual(dpy, DefaultScreen(dpy)),
			ctx->depth, ZPixmap, NULL,
			&ctx->shminfo, ctx->width, ctx->height);

		if (ctx->shmImage) {
			ctx->shminfo.shmid = shmget(IPC_PRIVATE,
				ctx->shmImage->bytes_per_line * ctx->shmImage->height,
				IPC_CREAT | 0600);

			if (ctx->shminfo.shmid >= 0) {
				ctx->shminfo.shmaddr = (char*)shmat(ctx->shminfo.shmid, NULL, 0);
				ctx->shmImage->data = ctx->shminfo.shmaddr;
				ctx->shminfo.readOnly = False;

				if (XShmAttach(dpy, &ctx->shminfo)) {
					ctx->useShm = 1;
					// Mark for removal after detach
					shmctl(ctx->shminfo.shmid, IPC_RMID, NULL);
				} else {
					shmdt(ctx->shminfo.shmaddr);
					shmctl(ctx->shminfo.shmid, IPC_RMID, NULL);
					XDestroyImage(ctx->shmImage);
					ctx->shmImage = NULL;
				}
			} else {
				XDestroyImage(ctx->shmImage);
				ctx->shmImage = NULL;
			}
		}
	}

	return ctx;
}

// x11_capture_frame_into captures the screen directly into the caller-provided
// buffer dst (width*height*4 bytes, tight stride = width*4). This eliminates
// the intermediate malloc and the extra Go-side allocation that the old API
// required. The function handles source images with non-tight strides by
// copying row-by-row.
static int x11_capture_frame_into(x11_capture_t *ctx, unsigned char *dst, int dst_width, int dst_height) {
	if (!ctx || !ctx->dpy || !dst) return -1;

	XImage *img = NULL;

	if (ctx->useShm && ctx->shmImage) {
		XShmGetImage(ctx->dpy, ctx->root, ctx->shmImage, 0, 0, AllPlanes);
		img = ctx->shmImage;
	} else {
		if (ctx->fallbackImage) {
			XDestroyImage(ctx->fallbackImage);
			ctx->fallbackImage = NULL;
		}
		img = XGetImage(ctx->dpy, ctx->root, 0, 0, ctx->width, ctx->height, AllPlanes, ZPixmap);
		if (!img) return -1;
		ctx->fallbackImage = img;
	}

	// X11 ZPixmap with depth 24/32 gives us pixels in BGRX or BGRA format (on little-endian).
	// The server expects BGRA, so we just need to ensure alpha = 255.
	int src_stride = img->bytes_per_line;
	int dst_stride = dst_width * 4;
	int rows = dst_height < ctx->height ? dst_height : ctx->height;

	if (src_stride == dst_stride) {
		// Fast path: source and destination strides match — single copy.
		memcpy(dst, img->data, (size_t)dst_stride * (size_t)rows);
	} else {
		// Row-by-row copy to handle GPU-aligned source strides.
		int copy_bytes = src_stride < dst_stride ? src_stride : dst_stride;
		for (int row = 0; row < rows; row++) {
			memcpy(dst + row * dst_stride, img->data + row * src_stride, copy_bytes);
		}
	}

	// Set alpha channel to 255 in-place if 32-bit pixels.
	if (img->bits_per_pixel == 32) {
		int size = dst_stride * rows;
		for (int i = 3; i < size; i += 4) {
			dst[i] = 255;
		}
	}

	return 0;
}

static void x11_capture_close(x11_capture_t *ctx) {
	if (!ctx) return;

	if (ctx->fallbackImage) {
		XDestroyImage(ctx->fallbackImage);
	}

	if (ctx->useShm && ctx->shmImage) {
		XShmDetach(ctx->dpy, &ctx->shminfo);
		shmdt(ctx->shminfo.shmaddr);
		XDestroyImage(ctx->shmImage);
	}

	if (ctx->dpy) {
		XCloseDisplay(ctx->dpy);
	}

	free(ctx);
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// X11Capture captures the screen using X11/XShm.
type X11Capture struct {
	ctx    *C.x11_capture_t
	width  uint16
	height uint16
	pixels []byte // persistent pixel buffer — allocated once in Init, reused every frame
	stride int
}

// newX11Capture returns a new X11-based screen capturer. It is always
// available on Linux builds; the linked libraries (libX11, libXext) must
// be present on the system at runtime.
func newX11Capture() (ScreenCapture, error) {
	return &X11Capture{}, nil
}

func (x *X11Capture) Init() error {
	var w, h C.int
	ctx := C.x11_capture_init(&w, &h)
	if ctx == nil {
		return fmt.Errorf("failed to open X11 display (is DISPLAY set?)")
	}
	x.ctx = ctx
	x.width = uint16(w)
	x.height = uint16(h)
	x.stride = int(w) * 4
	x.pixels = make([]byte, int(w)*int(h)*4)
	return nil
}

func (x *X11Capture) Bounds() (uint16, uint16) {
	return x.width, x.height
}

func (x *X11Capture) Capture() ([]byte, int, error) {
	if x.ctx == nil {
		return nil, 0, fmt.Errorf("X11 capture not initialized")
	}

	rc := C.x11_capture_frame_into(x.ctx,
		(*C.uchar)(unsafe.Pointer(&x.pixels[0])),
		C.int(x.width), C.int(x.height))
	if rc != 0 {
		return nil, 0, fmt.Errorf("X11 screen capture failed")
	}

	return x.pixels, x.stride, nil
}

func (x *X11Capture) Close() error {
	if x.ctx != nil {
		C.x11_capture_close(x.ctx)
		x.ctx = nil
	}
	return nil
}
