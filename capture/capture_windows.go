//go:build windows

package capture

/*
#cgo LDFLAGS: -ld3d11 -ldxgi -lole32

#include <stdint.h>
#include <stdlib.h>
#include <string.h>

// We use raw COM vtable calls to avoid requiring the full DirectX SDK headers.
// This keeps the CGo build self-contained.

#define COBJMACROS
#include <windows.h>
#include <d3d11.h>
#include <dxgi1_2.h>

typedef struct {
	ID3D11Device          *device;
	ID3D11DeviceContext   *context;
	IDXGIOutputDuplication *duplication;
	ID3D11Texture2D       *stagingTex;
	int                    width;
	int                    height;
} dxgi_capture_t;

static dxgi_capture_t* dxgi_capture_init(int *out_w, int *out_h) {
	HRESULT hr;

	// Create D3D11 device
	ID3D11Device *device = NULL;
	ID3D11DeviceContext *context = NULL;
	D3D_FEATURE_LEVEL featureLevel;
	D3D_FEATURE_LEVEL featureLevels[] = { D3D_FEATURE_LEVEL_11_0, D3D_FEATURE_LEVEL_10_1 };

	hr = D3D11CreateDevice(
		NULL,                       // default adapter
		D3D_DRIVER_TYPE_HARDWARE,
		NULL,                       // no software rasterizer
		0,                          // flags
		featureLevels,
		2,
		D3D11_SDK_VERSION,
		&device,
		&featureLevel,
		&context
	);
	if (FAILED(hr)) return NULL;

	// Get DXGI device
	IDXGIDevice *dxgiDevice = NULL;
	hr = ID3D11Device_QueryInterface(device, &IID_IDXGIDevice, (void**)&dxgiDevice);
	if (FAILED(hr)) {
		ID3D11Device_Release(device);
		ID3D11DeviceContext_Release(context);
		return NULL;
	}

	// Get DXGI adapter
	IDXGIAdapter *adapter = NULL;
	hr = IDXGIDevice_GetAdapter(dxgiDevice, &adapter);
	IDXGIDevice_Release(dxgiDevice);
	if (FAILED(hr)) {
		ID3D11Device_Release(device);
		ID3D11DeviceContext_Release(context);
		return NULL;
	}

	// Get first output
	IDXGIOutput *output = NULL;
	hr = IDXGIAdapter_EnumOutputs(adapter, 0, &output);
	IDXGIAdapter_Release(adapter);
	if (FAILED(hr)) {
		ID3D11Device_Release(device);
		ID3D11DeviceContext_Release(context);
		return NULL;
	}

	// Get output description for dimensions
	DXGI_OUTPUT_DESC outputDesc;
	IDXGIOutput_GetDesc(output, &outputDesc);
	int width = outputDesc.DesktopCoordinates.right - outputDesc.DesktopCoordinates.left;
	int height = outputDesc.DesktopCoordinates.bottom - outputDesc.DesktopCoordinates.top;

	// QI for IDXGIOutput1 to get DuplicateOutput
	IDXGIOutput1 *output1 = NULL;
	hr = IDXGIOutput_QueryInterface(output, &IID_IDXGIOutput1, (void**)&output1);
	IDXGIOutput_Release(output);
	if (FAILED(hr)) {
		ID3D11Device_Release(device);
		ID3D11DeviceContext_Release(context);
		return NULL;
	}

	// Create desktop duplication
	IDXGIOutputDuplication *duplication = NULL;
	hr = IDXGIOutput1_DuplicateOutput(output1, (IUnknown*)device, &duplication);
	IDXGIOutput1_Release(output1);
	if (FAILED(hr)) {
		ID3D11Device_Release(device);
		ID3D11DeviceContext_Release(context);
		return NULL;
	}

	// Create a CPU-accessible staging texture for reading back pixels
	D3D11_TEXTURE2D_DESC stagingDesc;
	memset(&stagingDesc, 0, sizeof(stagingDesc));
	stagingDesc.Width = width;
	stagingDesc.Height = height;
	stagingDesc.MipLevels = 1;
	stagingDesc.ArraySize = 1;
	stagingDesc.Format = DXGI_FORMAT_B8G8R8A8_UNORM;
	stagingDesc.SampleDesc.Count = 1;
	stagingDesc.Usage = D3D11_USAGE_STAGING;
	stagingDesc.CPUAccessFlags = D3D11_CPU_ACCESS_READ;

	ID3D11Texture2D *stagingTex = NULL;
	hr = ID3D11Device_CreateTexture2D(device, &stagingDesc, NULL, &stagingTex);
	if (FAILED(hr)) {
		IDXGIOutputDuplication_Release(duplication);
		ID3D11Device_Release(device);
		ID3D11DeviceContext_Release(context);
		return NULL;
	}

	dxgi_capture_t *ctx = (dxgi_capture_t*)calloc(1, sizeof(dxgi_capture_t));
	ctx->device = device;
	ctx->context = context;
	ctx->duplication = duplication;
	ctx->stagingTex = stagingTex;
	ctx->width = width;
	ctx->height = height;

	*out_w = width;
	*out_h = height;
	return ctx;
}

static int dxgi_capture_frame(dxgi_capture_t *ctx, unsigned char **out_pixels, int *out_stride) {
	if (!ctx || !ctx->duplication) return -1;

	HRESULT hr;
	IDXGIResource *desktopResource = NULL;
	DXGI_OUTDUPL_FRAME_INFO frameInfo;

	// Acquire the next frame (timeout 500ms)
	hr = IDXGIOutputDuplication_AcquireNextFrame(ctx->duplication, 500, &frameInfo, &desktopResource);
	if (FAILED(hr)) {
		// If access lost, return error so caller can reinit
		return -1;
	}

	// Get the ID3D11Texture2D from the desktop resource
	ID3D11Texture2D *desktopTex = NULL;
	hr = IDXGIResource_QueryInterface(desktopResource, &IID_ID3D11Texture2D, (void**)&desktopTex);
	IDXGIResource_Release(desktopResource);
	if (FAILED(hr)) {
		IDXGIOutputDuplication_ReleaseFrame(ctx->duplication);
		return -1;
	}

	// Copy the desktop texture to our staging texture
	ID3D11DeviceContext_CopyResource(ctx->context, (ID3D11Resource*)ctx->stagingTex, (ID3D11Resource*)desktopTex);
	ID3D11Texture2D_Release(desktopTex);

	// Map the staging texture to read pixels
	D3D11_MAPPED_SUBRESOURCE mapped;
	hr = ID3D11DeviceContext_Map(ctx->context, (ID3D11Resource*)ctx->stagingTex, 0, D3D11_MAP_READ, 0, &mapped);
	if (FAILED(hr)) {
		IDXGIOutputDuplication_ReleaseFrame(ctx->duplication);
		return -1;
	}

	int stride = ctx->width * 4;
	int size = stride * ctx->height;
	unsigned char *buf = (unsigned char*)malloc(size);
	if (!buf) {
		ID3D11DeviceContext_Unmap(ctx->context, (ID3D11Resource*)ctx->stagingTex, 0);
		IDXGIOutputDuplication_ReleaseFrame(ctx->duplication);
		return -1;
	}

	// Copy row-by-row since mapped.RowPitch may differ from our stride
	unsigned char *src = (unsigned char*)mapped.pData;
	for (int y = 0; y < ctx->height; y++) {
		memcpy(buf + y * stride, src + y * mapped.RowPitch, stride);
	}

	// Ensure alpha is 255 (DXGI gives BGRA with alpha that may not be 0xFF)
	for (int i = 3; i < size; i += 4) {
		buf[i] = 255;
	}

	ID3D11DeviceContext_Unmap(ctx->context, (ID3D11Resource*)ctx->stagingTex, 0);
	IDXGIOutputDuplication_ReleaseFrame(ctx->duplication);

	*out_pixels = buf;
	*out_stride = stride;
	return 0;
}

static void dxgi_capture_close(dxgi_capture_t *ctx) {
	if (!ctx) return;

	if (ctx->stagingTex) {
		ID3D11Texture2D_Release(ctx->stagingTex);
	}
	if (ctx->duplication) {
		IDXGIOutputDuplication_Release(ctx->duplication);
	}
	if (ctx->context) {
		ID3D11DeviceContext_Release(ctx->context);
	}
	if (ctx->device) {
		ID3D11Device_Release(ctx->device);
	}

	free(ctx);
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// DXGICapture captures the screen using the DXGI Desktop Duplication API.
type DXGICapture struct {
	ctx    *C.dxgi_capture_t
	width  uint16
	height uint16
}

func NewScreenCapture() (ScreenCapture, error) {
	return &DXGICapture{}, nil
}

func (d *DXGICapture) Init() error {
	var w, h C.int
	ctx := C.dxgi_capture_init(&w, &h)
	if ctx == nil {
		return fmt.Errorf("failed to initialize DXGI Desktop Duplication (is a desktop session active?)")
	}
	d.ctx = ctx
	d.width = uint16(w)
	d.height = uint16(h)
	return nil
}

func (d *DXGICapture) Bounds() (uint16, uint16) {
	return d.width, d.height
}

func (d *DXGICapture) Capture() ([]byte, int, error) {
	if d.ctx == nil {
		return nil, 0, fmt.Errorf("DXGI capture not initialized")
	}

	var buf *C.uchar
	var stride C.int

	rc := C.dxgi_capture_frame(d.ctx, &buf, &stride)
	if rc != 0 {
		return nil, 0, fmt.Errorf("DXGI screen capture failed")
	}
	defer C.free(unsafe.Pointer(buf))

	size := int(stride) * int(d.height)
	pixels := make([]byte, size)
	copy(pixels, unsafe.Slice((*byte)(unsafe.Pointer(buf)), size))

	return pixels, int(stride), nil
}

func (d *DXGICapture) Close() error {
	if d.ctx != nil {
		C.dxgi_capture_close(d.ctx)
		d.ctx = nil
	}
	return nil
}
