//go:build cgo && !windows

package h264

/*
#cgo pkg-config: x264
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <x264.h>

typedef struct {
	x264_t          *enc;
	x264_picture_t   pic_in;
	x264_picture_t   pic_out;
	int              width;
	int              height;
} x264_ctx_t;

static x264_ctx_t* x264_ctx_open(int width, int height) {
	x264_ctx_t *ctx = (x264_ctx_t*)calloc(1, sizeof(x264_ctx_t));
	if (!ctx) return NULL;

	x264_param_t param;
	x264_param_default_preset(&param, "ultrafast", "zerolatency");

	param.i_width  = width;
	param.i_height = height;
	param.i_csp    = X264_CSP_NV12;

	// Baseline profile for widest WebCodecs compatibility.
	x264_param_apply_profile(&param, "baseline");

	// Rate control: CRF 23 (good quality, adapts to content).
	param.rc.i_rc_method = X264_RC_CRF;
	param.rc.f_rf_constant = 23.0;

	// Keyframe interval.
	param.i_keyint_max = 150;   // IDR every ~5s at 30fps
	param.i_keyint_min = 1;

	// No B-frames (forced by zerolatency tune).
	param.i_bframe = 0;

	// Auto-detect threads.
	param.i_threads = 0;

	// Repeat SPS/PPS before each IDR for stream robustness.
	param.b_repeat_headers = 1;

	// Annex B format (start codes).
	param.b_annexb = 1;

	ctx->enc = x264_encoder_open(&param);
	if (!ctx->enc) {
		free(ctx);
		return NULL;
	}

	x264_picture_init(&ctx->pic_in);
	ctx->pic_in.img.i_csp    = X264_CSP_NV12;
	ctx->pic_in.img.i_plane  = 2;
	ctx->pic_in.img.i_stride[0] = width;       // Y stride
	ctx->pic_in.img.i_stride[1] = width;       // UV stride (interleaved)
	ctx->width  = width;
	ctx->height = height;

	return ctx;
}

// x264_ctx_encode encodes one NV12 frame. Returns the number of NAL bytes
// written to out_buf, or negative on error. Sets *is_keyframe to 1 if IDR.
static int x264_ctx_encode(x264_ctx_t *ctx, uint8_t *nv12, int force_idr,
                           uint8_t **out_nal, int *is_keyframe) {
	ctx->pic_in.img.plane[0] = nv12;                                     // Y
	ctx->pic_in.img.plane[1] = nv12 + ctx->width * ctx->height;         // UV
	ctx->pic_in.i_type = force_idr ? X264_TYPE_IDR : X264_TYPE_AUTO;
	ctx->pic_in.i_pts++;

	x264_nal_t *nals;
	int n_nal;
	int frame_size = x264_encoder_encode(ctx->enc, &nals, &n_nal, &ctx->pic_in, &ctx->pic_out);
	if (frame_size < 0) {
		return -1;
	}

	*is_keyframe = (ctx->pic_out.i_type == X264_TYPE_IDR) ? 1 : 0;

	if (frame_size == 0 || n_nal == 0) {
		*out_nal = NULL;
		return 0;
	}

	// nals[0].p_payload points to a contiguous buffer covering all NALs.
	*out_nal = nals[0].p_payload;
	return frame_size;
}

static void x264_ctx_close(x264_ctx_t *ctx) {
	if (ctx) {
		if (ctx->enc) {
			x264_encoder_close(ctx->enc);
		}
		free(ctx);
	}
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

type x264Backend struct {
	ctx *C.x264_ctx_t
}

func newBackend(width, height int) (h264Backend, error) {
	ctx := C.x264_ctx_open(C.int(width), C.int(height))
	if ctx == nil {
		return nil, fmt.Errorf("x264_encoder_open failed")
	}
	return &x264Backend{ctx: ctx}, nil
}

func (b *x264Backend) Encode(nv12 []byte, forceIDR bool) ([]byte, bool, error) {
	var outNAL *C.uint8_t
	var isKeyframe C.int
	forceIDRInt := C.int(0)
	if forceIDR {
		forceIDRInt = 1
	}

	frameSize := C.x264_ctx_encode(b.ctx,
		(*C.uint8_t)(unsafe.Pointer(&nv12[0])),
		forceIDRInt,
		&outNAL,
		&isKeyframe)

	if frameSize < 0 {
		return nil, false, fmt.Errorf("x264_encoder_encode failed")
	}
	if frameSize == 0 || outNAL == nil {
		return nil, false, nil
	}

	// Copy NAL data to Go-managed memory.
	nalData := C.GoBytes(unsafe.Pointer(outNAL), frameSize)
	return nalData, isKeyframe != 0, nil
}

func (b *x264Backend) Close() {
	if b.ctx != nil {
		C.x264_ctx_close(b.ctx)
		b.ctx = nil
	}
}
