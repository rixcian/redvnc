//go:build linux

package input

/*
#cgo LDFLAGS: -lX11 -lXtst
#include <X11/Xlib.h>
#include <X11/keysym.h>
#include <X11/extensions/XTest.h>
#include <stdlib.h>

typedef struct {
	Display *dpy;
} x11_input_t;

static x11_input_t* x11_input_init() {
	Display *dpy = XOpenDisplay(NULL);
	if (!dpy) return NULL;

	int event_base, error_base, major, minor;
	if (!XTestQueryExtension(dpy, &event_base, &error_base, &major, &minor)) {
		XCloseDisplay(dpy);
		return NULL;
	}

	x11_input_t *ctx = (x11_input_t*)calloc(1, sizeof(x11_input_t));
	ctx->dpy = dpy;
	return ctx;
}

static int x11_key_event(x11_input_t *ctx, unsigned int keysym, int down) {
	if (!ctx || !ctx->dpy) return -1;

	KeyCode kc = XKeysymToKeycode(ctx->dpy, keysym);
	if (kc == 0) return -1;

	XTestFakeKeyEvent(ctx->dpy, kc, down ? True : False, CurrentTime);
	XFlush(ctx->dpy);
	return 0;
}

static int x11_pointer_move(x11_input_t *ctx, int x, int y) {
	if (!ctx || !ctx->dpy) return -1;

	XTestFakeMotionEvent(ctx->dpy, DefaultScreen(ctx->dpy), x, y, CurrentTime);
	XFlush(ctx->dpy);
	return 0;
}

static int x11_pointer_button(x11_input_t *ctx, unsigned int button, int down) {
	if (!ctx || !ctx->dpy) return -1;

	XTestFakeButtonEvent(ctx->dpy, button, down ? True : False, CurrentTime);
	XFlush(ctx->dpy);
	return 0;
}

static void x11_input_close(x11_input_t *ctx) {
	if (!ctx) return;
	if (ctx->dpy) XCloseDisplay(ctx->dpy);
	free(ctx);
}
*/
import "C"

import "fmt"

// X11Input injects input using X11 XTest extension.
type X11Input struct {
	ctx            *C.x11_input_t
	lastButtonMask uint8
}

// newX11Input returns a new X11/XTest-based input injector. It is always
// available on Linux builds; the linked libraries (libX11, libXtst) must
// be present on the system at runtime.
func newX11Input() (InputInjector, error) {
	return &X11Input{}, nil
}

func (x *X11Input) Init() error {
	ctx := C.x11_input_init()
	if ctx == nil {
		return fmt.Errorf("failed to initialize X11 input (is DISPLAY set? is XTest available?)")
	}
	x.ctx = ctx
	return nil
}

func (x *X11Input) KeyEvent(down bool, key uint32) error {
	if x.ctx == nil {
		return fmt.Errorf("X11 input not initialized")
	}

	downInt := C.int(0)
	if down {
		downInt = 1
	}

	// VNC uses X11 keysyms directly, so we can pass them straight through
	rc := C.x11_key_event(x.ctx, C.uint(key), downInt)
	if rc != 0 {
		// Unknown keysym — ignore rather than error
		return nil
	}
	return nil
}

func (x *X11Input) PointerEvent(buttonMask uint8, xPos, yPos uint16) error {
	if x.ctx == nil {
		return fmt.Errorf("X11 input not initialized")
	}

	// Move the pointer
	C.x11_pointer_move(x.ctx, C.int(xPos), C.int(yPos))

	// Detect button state changes
	changed := x.lastButtonMask ^ buttonMask

	// VNC button mask: bit 0 = left (X11 button 1), bit 1 = middle (button 2),
	// bit 2 = right (button 3), bit 3 = scroll up (button 4), bit 4 = scroll down (button 5)
	buttonMap := [5]C.uint{1, 2, 3, 4, 5}

	for i := uint8(0); i < 5; i++ {
		if changed&(1<<i) != 0 {
			downInt := C.int(0)
			if buttonMask&(1<<i) != 0 {
				downInt = 1
			}
			C.x11_pointer_button(x.ctx, buttonMap[i], downInt)
		}
	}

	x.lastButtonMask = buttonMask
	return nil
}

func (x *X11Input) Close() error {
	if x.ctx != nil {
		C.x11_input_close(x.ctx)
		x.ctx = nil
	}
	return nil
}
