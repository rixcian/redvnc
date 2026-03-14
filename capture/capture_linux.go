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

static int x11_capture_frame(x11_capture_t *ctx, unsigned char **out_pixels, int *out_stride) {
	if (!ctx || !ctx->dpy) return -1;

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
	int stride = img->bytes_per_line;
	int size = stride * ctx->height;

	unsigned char *buf = (unsigned char*)malloc(size);
	if (!buf) return -1;

	memcpy(buf, img->data, size);

	// Set alpha channel to 255 if 32-bit pixels
	if (img->bits_per_pixel == 32) {
		for (int i = 3; i < size; i += 4) {
			buf[i] = 255;
		}
	}

	*out_pixels = buf;
	*out_stride = stride;
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
}

func NewScreenCapture() (ScreenCapture, error) {
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
	return nil
}

func (x *X11Capture) Bounds() (uint16, uint16) {
	return x.width, x.height
}

func (x *X11Capture) Capture() ([]byte, int, error) {
	if x.ctx == nil {
		return nil, 0, fmt.Errorf("X11 capture not initialized")
	}

	var buf *C.uchar
	var stride C.int

	rc := C.x11_capture_frame(x.ctx, &buf, &stride)
	if rc != 0 {
		return nil, 0, fmt.Errorf("X11 screen capture failed")
	}
	defer C.free(unsafe.Pointer(buf))

	size := int(stride) * int(x.height)
	pixels := make([]byte, size)
	copy(pixels, unsafe.Slice((*byte)(unsafe.Pointer(buf)), size))

	return pixels, int(stride), nil
}

func (x *X11Capture) Close() error {
	if x.ctx != nil {
		C.x11_capture_close(x.ctx)
		x.ctx = nil
	}
	return nil
}
