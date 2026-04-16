//go:build linux && wayland

// This file implements screen capture on Wayland using PipeWire as the
// video transport and xdg-desktop-portal (ScreenCast) as the session
// authority. It requires:
//
//   - libpipewire-0.3 and libspa-0.2 headers at compile time
//     (Debian/Ubuntu: libpipewire-0.3-dev; Fedora: pipewire-devel)
//   - A running user-bus instance of xdg-desktop-portal with a
//     backend that supports ScreenCast (GNOME, KDE, xdg-desktop-portal-wlr)
//
// Build with: go build -tags wayland ./...
package capture

/*
#cgo pkg-config: libpipewire-0.3

#define _GNU_SOURCE
#include <pipewire/pipewire.h>
#include <spa/param/video/format-utils.h>
#include <spa/param/video/type-info.h>
#include <spa/param/format-utils.h>
#include <spa/utils/result.h>
#include <spa/debug/types.h>

#include <pthread.h>
#include <stdlib.h>
#include <string.h>
#include <errno.h>

// pw_capture_t owns a single PipeWire video stream connected to the
// node id returned by the portal. Frames are copied into a shared
// buffer that the Go side reads with pw_capture_read().
typedef struct {
	struct pw_thread_loop *loop;
	struct pw_context    *context;
	struct pw_core       *core;
	struct pw_stream     *stream;
	struct spa_hook       stream_listener;

	uint32_t node_id;

	// Negotiated format.
	int width;
	int height;
	int stride;
	uint32_t spa_format;

	// Frame buffer. Allocated lazily in on_param_changed once the
	// format is known, then reused for every frame.
	pthread_mutex_t lock;
	unsigned char *frame;
	size_t frame_cap;
	int frame_ready;

	// Set by the stream error callback when the stream fails.
	int fatal;
} pw_capture_t;

// Forward declarations for C static callbacks.
static void on_stream_state_changed(void *userdata, enum pw_stream_state old,
                                    enum pw_stream_state state, const char *error);
static void on_stream_param_changed(void *userdata, uint32_t id, const struct spa_pod *param);
static void on_stream_process(void *userdata);

static const struct pw_stream_events stream_events = {
	PW_VERSION_STREAM_EVENTS,
	.state_changed = on_stream_state_changed,
	.param_changed = on_stream_param_changed,
	.process       = on_stream_process,
};

static void on_stream_state_changed(void *userdata, enum pw_stream_state old,
                                    enum pw_stream_state state, const char *error) {
	pw_capture_t *c = (pw_capture_t*)userdata;
	(void)old;
	if (state == PW_STREAM_STATE_ERROR) {
		c->fatal = 1;
		pw_thread_loop_signal(c->loop, false);
	}
}

static void on_stream_param_changed(void *userdata, uint32_t id, const struct spa_pod *param) {
	pw_capture_t *c = (pw_capture_t*)userdata;
	if (param == NULL || id != SPA_PARAM_Format) {
		return;
	}

	struct spa_video_info info = {0};
	if (spa_format_parse(param, &info.media_type, &info.media_subtype) < 0) return;
	if (info.media_type != SPA_MEDIA_TYPE_video) return;
	if (info.media_subtype != SPA_MEDIA_SUBTYPE_raw) return;

	if (spa_format_video_raw_parse(param, &info.info.raw) < 0) return;

	c->spa_format = info.info.raw.format;
	c->width  = info.info.raw.size.width;
	c->height = info.info.raw.size.height;
	// 4 bytes/pixel for BGRx/BGRA/xBGR/RGBA/RGBx.
	c->stride = c->width * 4;

	pthread_mutex_lock(&c->lock);
	size_t needed = (size_t)c->stride * (size_t)c->height;
	if (needed > c->frame_cap) {
		free(c->frame);
		c->frame = (unsigned char*)malloc(needed);
		c->frame_cap = c->frame ? needed : 0;
	}
	c->frame_ready = 0;
	pthread_mutex_unlock(&c->lock);

	// Tell PipeWire we want ~8 buffers of stride*height bytes. The
	// compositor ultimately decides the layout.
	uint8_t pod_buf[1024];
	struct spa_pod_builder b = SPA_POD_BUILDER_INIT(pod_buf, sizeof(pod_buf));
	const struct spa_pod *params[1];
	params[0] = spa_pod_builder_add_object(&b,
		SPA_TYPE_OBJECT_ParamBuffers, SPA_PARAM_Buffers,
		SPA_PARAM_BUFFERS_buffers, SPA_POD_CHOICE_RANGE_Int(4, 2, 16),
		SPA_PARAM_BUFFERS_blocks,  SPA_POD_Int(1),
		SPA_PARAM_BUFFERS_size,    SPA_POD_Int(c->stride * c->height),
		SPA_PARAM_BUFFERS_stride,  SPA_POD_Int(c->stride),
		SPA_PARAM_BUFFERS_align,   SPA_POD_Int(16)
	);
	pw_stream_update_params(c->stream, params, 1);
}

static void on_stream_process(void *userdata) {
	pw_capture_t *c = (pw_capture_t*)userdata;
	struct pw_buffer *b = pw_stream_dequeue_buffer(c->stream);
	if (b == NULL) return;

	struct spa_buffer *sbuf = b->buffer;
	if (sbuf->n_datas == 0 || sbuf->datas[0].data == NULL) {
		pw_stream_queue_buffer(c->stream, b);
		return;
	}

	struct spa_data *d = &sbuf->datas[0];
	unsigned char *src = (unsigned char*)d->data;

	// PipeWire reports the valid region via chunk offset/size/stride.
	int32_t src_stride = d->chunk->stride > 0 ? d->chunk->stride : c->stride;
	int32_t src_offset = d->chunk->offset;
	src = src + src_offset;

	pthread_mutex_lock(&c->lock);
	if (c->frame != NULL && c->width > 0 && c->height > 0) {
		int rows = c->height;
		int copy_bytes = src_stride < c->stride ? src_stride : c->stride;
		if (src_stride == c->stride) {
			memcpy(c->frame, src, (size_t)c->stride * (size_t)rows);
		} else {
			for (int y = 0; y < rows; y++) {
				memcpy(c->frame + y * c->stride, src + y * src_stride, copy_bytes);
			}
		}
		c->frame_ready = 1;
	}
	pthread_mutex_unlock(&c->lock);

	pw_stream_queue_buffer(c->stream, b);
}

// build_format_pod returns a POD describing the video formats we are
// willing to accept. BGRx/BGRA match the X11 path exactly; we list them
// first so compositors that offer multiple formats will prefer them.
static const struct spa_pod *build_format_pod(struct spa_pod_builder *b) {
	return spa_pod_builder_add_object(b,
		SPA_TYPE_OBJECT_Format, SPA_PARAM_EnumFormat,
		SPA_FORMAT_mediaType,    SPA_POD_Id(SPA_MEDIA_TYPE_video),
		SPA_FORMAT_mediaSubtype, SPA_POD_Id(SPA_MEDIA_SUBTYPE_raw),
		SPA_FORMAT_VIDEO_format, SPA_POD_CHOICE_ENUM_Id(4,
			SPA_VIDEO_FORMAT_BGRx,
			SPA_VIDEO_FORMAT_BGRx,
			SPA_VIDEO_FORMAT_BGRA,
			SPA_VIDEO_FORMAT_RGBx),
		SPA_FORMAT_VIDEO_size,      SPA_POD_CHOICE_RANGE_Rectangle(
			&SPA_RECTANGLE(1920, 1080),
			&SPA_RECTANGLE(1, 1),
			&SPA_RECTANGLE(8192, 8192)),
		SPA_FORMAT_VIDEO_framerate, SPA_POD_CHOICE_RANGE_Fraction(
			&SPA_FRACTION(60, 1),
			&SPA_FRACTION(1, 1),
			&SPA_FRACTION(240, 1))
	);
}

// pw_capture_init connects a PipeWire stream to the given node id.
// pw_fd is a file descriptor returned by the portal OpenPipeWireRemote
// call. It is consumed (owned) by the returned context.
static pw_capture_t *pw_capture_init(uint32_t node_id, int pw_fd) {
	// pw_init is idempotent; calling it multiple times is safe.
	pw_init(NULL, NULL);

	pw_capture_t *c = (pw_capture_t*)calloc(1, sizeof(pw_capture_t));
	if (c == NULL) {
		if (pw_fd >= 0) close(pw_fd);
		return NULL;
	}
	pthread_mutex_init(&c->lock, NULL);
	c->node_id = node_id;

	c->loop = pw_thread_loop_new("redvnc-capture", NULL);
	if (c->loop == NULL) goto fail;

	pw_thread_loop_lock(c->loop);

	c->context = pw_context_new(pw_thread_loop_get_loop(c->loop), NULL, 0);
	if (c->context == NULL) { pw_thread_loop_unlock(c->loop); goto fail; }

	if (pw_fd >= 0) {
		c->core = pw_context_connect_fd(c->context, pw_fd, NULL, 0);
	} else {
		c->core = pw_context_connect(c->context, NULL, 0);
	}
	if (c->core == NULL) { pw_thread_loop_unlock(c->loop); goto fail; }

	struct pw_properties *props = pw_properties_new(
		PW_KEY_MEDIA_TYPE,     "Video",
		PW_KEY_MEDIA_CATEGORY, "Capture",
		PW_KEY_MEDIA_ROLE,     "Screen",
		NULL);
	c->stream = pw_stream_new(c->core, "redvnc-capture", props);
	if (c->stream == NULL) { pw_thread_loop_unlock(c->loop); goto fail; }

	pw_stream_add_listener(c->stream, &c->stream_listener, &stream_events, c);

	uint8_t pod_buf[1024];
	struct spa_pod_builder b = SPA_POD_BUILDER_INIT(pod_buf, sizeof(pod_buf));
	const struct spa_pod *params[1] = { build_format_pod(&b) };

	int rc = pw_stream_connect(c->stream,
		PW_DIRECTION_INPUT, c->node_id,
		PW_STREAM_FLAG_AUTOCONNECT | PW_STREAM_FLAG_MAP_BUFFERS,
		params, 1);
	pw_thread_loop_unlock(c->loop);
	if (rc < 0) goto fail;

	if (pw_thread_loop_start(c->loop) < 0) goto fail;

	return c;

fail:
	if (c->stream)   { pw_stream_destroy(c->stream); c->stream = NULL; }
	if (c->core)     { pw_core_disconnect(c->core); c->core = NULL; }
	if (c->context)  { pw_context_destroy(c->context); c->context = NULL; }
	if (c->loop)     { pw_thread_loop_destroy(c->loop); c->loop = NULL; }
	pthread_mutex_destroy(&c->lock);
	free(c);
	return NULL;
}

// pw_capture_get_size returns the negotiated width/height/stride or -1
// if the format is not yet known.
static int pw_capture_get_size(pw_capture_t *c, int *w, int *h, int *stride) {
	if (c == NULL) return -1;
	pthread_mutex_lock(&c->lock);
	int ok = c->width > 0 && c->height > 0;
	if (ok) {
		*w = c->width; *h = c->height; *stride = c->stride;
	}
	pthread_mutex_unlock(&c->lock);
	return ok ? 0 : -1;
}

// pw_capture_read copies the latest frame into dst. Returns 0 on
// success, 1 if no frame has been received yet, -1 on fatal error. The
// caller must pass a buffer sized dst_w*dst_h*4.
static int pw_capture_read(pw_capture_t *c, unsigned char *dst, int dst_w, int dst_h) {
	if (c == NULL || dst == NULL) return -1;
	if (c->fatal) return -1;

	int rc;
	pthread_mutex_lock(&c->lock);
	if (!c->frame_ready || c->frame == NULL) {
		rc = 1;
	} else {
		int rows = dst_h < c->height ? dst_h : c->height;
		int row_bytes = dst_w * 4;
		if (row_bytes > c->stride) row_bytes = c->stride;
		if (row_bytes == c->stride && dst_w * 4 == c->stride) {
			memcpy(dst, c->frame, (size_t)row_bytes * (size_t)rows);
		} else {
			for (int y = 0; y < rows; y++) {
				memcpy(dst + y * dst_w * 4, c->frame + y * c->stride, row_bytes);
			}
		}
		// Normalize formats to BGRA by forcing alpha=255 when the
		// compositor gave us xBGR/BGRx (the X byte is undefined).
		int size = dst_w * 4 * rows;
		for (int i = 3; i < size; i += 4) dst[i] = 255;
		rc = 0;
	}
	pthread_mutex_unlock(&c->lock);
	return rc;
}

static void pw_capture_close(pw_capture_t *c) {
	if (c == NULL) return;
	if (c->loop) {
		pw_thread_loop_stop(c->loop);
	}
	if (c->stream) {
		pw_stream_destroy(c->stream);
		c->stream = NULL;
	}
	if (c->core) {
		pw_core_disconnect(c->core);
		c->core = NULL;
	}
	if (c->context) {
		pw_context_destroy(c->context);
		c->context = NULL;
	}
	if (c->loop) {
		pw_thread_loop_destroy(c->loop);
		c->loop = NULL;
	}
	pthread_mutex_destroy(&c->lock);
	free(c->frame);
	free(c);
}
*/
import "C"

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
	"unsafe"

	"github.com/rixcian/redvnc/internal/portal"
)

// WaylandCapture captures the screen using PipeWire via xdg-desktop-portal.
//
// If a shared portal session was already created by the input subsystem,
// it can be injected via NewWaylandCaptureWithSession; otherwise
// newWaylandCapture creates its own capture-only session.
type WaylandCapture struct {
	// session is the portal session that provides the PipeWire node.
	// It may be shared with input/WaylandInput.
	session    *portal.Session
	ownSession bool

	ctx    *C.pw_capture_t
	width  uint16
	height uint16
	stride int
	pixels []byte
}

// newWaylandCapture creates a capture-only Wayland screen capturer that
// owns its portal session. It is called by the factory in
// capture_linux.go when the "wayland" build tag is enabled.
func newWaylandCapture() (ScreenCapture, error) {
	return &WaylandCapture{}, nil
}

// NewWaylandCaptureWithSession creates a capturer that reuses an
// existing portal session. The capturer does not take ownership of the
// session: the caller must still Close it when both capture and input
// are finished with it.
func NewWaylandCaptureWithSession(s *portal.Session) (ScreenCapture, error) {
	if s == nil {
		return nil, errors.New("wayland capture: nil portal session")
	}
	return &WaylandCapture{session: s, ownSession: false}, nil
}

func (w *WaylandCapture) Init() error {
	if w.session == nil {
		s, err := portal.New(portal.SessionOpts{
			Capture:          true,
			RestoreTokenPath: defaultRestoreTokenPath(),
		})
		if err != nil {
			return fmt.Errorf("wayland capture: create portal session: %w", err)
		}
		w.session = s
		w.ownSession = true
	}

	nodeID, ok := w.session.PipeWireNodeID()
	if !ok {
		return errors.New("wayland capture: portal session has no PipeWire streams")
	}

	fd, err := w.session.OpenPipeWireRemote()
	if err != nil {
		return fmt.Errorf("wayland capture: OpenPipeWireRemote: %w", err)
	}

	ctx := C.pw_capture_init(C.uint32_t(nodeID), C.int(fd))
	if ctx == nil {
		return errors.New("wayland capture: pw_capture_init failed")
	}
	w.ctx = ctx

	// Wait for the format to be negotiated. Most portals deliver the
	// Format param within a few hundred milliseconds of connect.
	deadline := time.Now().Add(5 * time.Second)
	var cw, ch, cstride C.int
	for time.Now().Before(deadline) {
		if C.pw_capture_get_size(w.ctx, &cw, &ch, &cstride) == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if cw == 0 || ch == 0 {
		C.pw_capture_close(w.ctx)
		w.ctx = nil
		return errors.New("wayland capture: format negotiation timed out")
	}
	w.width = uint16(cw)
	w.height = uint16(ch)
	w.stride = int(cstride)
	w.pixels = make([]byte, int(cw)*int(ch)*4)
	return nil
}

func (w *WaylandCapture) Bounds() (uint16, uint16) {
	return w.width, w.height
}

// Capture returns the latest frame. If no frame has arrived since the
// previous Capture, it returns the previous pixel buffer (callers will
// see a repeated frame, which matches the X11 path's behaviour when the
// screen is static).
func (w *WaylandCapture) Capture() ([]byte, int, error) {
	if w.ctx == nil {
		return nil, 0, errors.New("wayland capture: not initialized")
	}
	rc := C.pw_capture_read(w.ctx,
		(*C.uchar)(unsafe.Pointer(&w.pixels[0])),
		C.int(w.width), C.int(w.height))
	switch rc {
	case 0:
		// New frame copied.
		return w.pixels, w.stride, nil
	case 1:
		// No new frame yet — return the previous one (all zeros on
		// first call). This keeps the server's framebuffer pipeline
		// moving while we wait for the compositor.
		return w.pixels, w.stride, nil
	default:
		return nil, 0, errors.New("wayland capture: stream failed")
	}
}

func (w *WaylandCapture) Close() error {
	if w.ctx != nil {
		C.pw_capture_close(w.ctx)
		w.ctx = nil
	}
	if w.ownSession && w.session != nil {
		_ = w.session.Close()
		w.session = nil
	}
	return nil
}

// --- misc -----------------------------------------------------------------

var restoreTokenPathOnce sync.Once
var restoreTokenPath string

// defaultRestoreTokenPath returns ~/.config/redvnc/portal-token, so the
// user is only prompted to grant screen-sharing permission once. If the
// home directory cannot be determined, returns an empty string (which
// portal.Session treats as "do not persist").
func defaultRestoreTokenPath() string {
	restoreTokenPathOnce.Do(func() {
		dir, err := os.UserConfigDir()
		if err != nil || dir == "" {
			return
		}
		restoreTokenPath = filepath.Join(dir, "redvnc", "portal-token")
	})
	return restoreTokenPath
}
