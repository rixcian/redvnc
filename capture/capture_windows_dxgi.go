//go:build windows

package capture

import (
	"fmt"
	"log"
	"sync"
	"syscall"
	"unsafe"
)

var (
	d3d11dll              = syscall.NewLazyDLL("d3d11.dll")
	procD3D11CreateDevice = d3d11dll.NewProc("D3D11CreateDevice")
)

// COM GUID structure.
type comGUID struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

// COM interface IIDs.
var (
	iidIDXGIDevice     = comGUID{0x54ec77fa, 0x1377, 0x44e6, [8]byte{0x8c, 0x32, 0x88, 0xfd, 0x5f, 0x44, 0xc8, 0x4c}}
	iidIDXGIOutput1    = comGUID{0x00cddea8, 0x939b, 0x4b83, [8]byte{0xa3, 0x40, 0xa6, 0x85, 0x22, 0x66, 0x66, 0xcc}}
	iidID3D11Texture2D = comGUID{0x6f15aaf2, 0xd208, 0x4e89, [8]byte{0x9a, 0xb4, 0x48, 0x95, 0x35, 0xd3, 0x4f, 0x9c}}
)

// D3D11/DXGI constants.
const (
	dxgiFormatB8G8R8A8UNorm  = 87
	d3d11SDKVersion          = 7
	d3dDriverTypeHardware    = 1
	d3d11UsageStaging        = 3
	d3d11CPUAccessRead       = 0x20000
	d3d11MapRead             = 1
	dxgiErrorWaitTimeout     = 0x887A0027
	dxgiErrorAccessLost      = 0x887A0026
)

// COM vtable method indices for each interface.
// Computed as: IUnknown(3) + parent interfaces + own methods.
const (
	// IDXGIDevice::GetAdapter (IUnknown=3 + IDXGIObject=4 + GetAdapter=0)
	methDXGIDeviceGetAdapter = 7
	// IDXGIAdapter::EnumOutputs (IUnknown=3 + IDXGIObject=4 + EnumOutputs=0)
	methAdapterEnumOutputs = 7
	// IDXGIOutput1::DuplicateOutput (IUnknown=3 + IDXGIObject=4 + IDXGIOutput=12 + 3)
	methOutput1DuplicateOutput = 22
	// IDXGIOutputDuplication::AcquireNextFrame (IUnknown=3 + IDXGIObject=4 + 1)
	methDuplAcquireNextFrame = 8
	// IDXGIOutputDuplication::ReleaseFrame (IUnknown=3 + IDXGIObject=4 + 7)
	methDuplReleaseFrame = 14
	// ID3D11Device::CreateTexture2D (IUnknown=3 + 2)
	methDeviceCreateTexture2D = 5
	// ID3D11DeviceContext::Map (IUnknown=3 + ID3D11DeviceChild=4 + 7)
	methCtxMap = 14
	// ID3D11DeviceContext::Unmap
	methCtxUnmap = 15
	// ID3D11DeviceContext::CopyResource
	methCtxCopyResource = 47
)

// D3D11/DXGI structures matching the C layout.

type dxgiSampleDesc struct {
	Count   uint32
	Quality uint32
}

type d3d11Texture2DDesc struct {
	Width          uint32
	Height         uint32
	MipLevels      uint32
	ArraySize      uint32
	Format         uint32
	SampleDesc     dxgiSampleDesc
	Usage          uint32
	BindFlags      uint32
	CPUAccessFlags uint32
	MiscFlags      uint32
}

type d3d11MappedSubresource struct {
	PData      uintptr
	RowPitch   uint32
	DepthPitch uint32
}

type dxgiOutduplFrameInfo struct {
	LastPresentTime           int64
	LastMouseUpdateTime       int64
	AccumulatedFrames         uint32
	RectsCoalesced            int32
	ProtectedContentMaskedOut int32
	PointerPositionX          int32
	PointerPositionY          int32
	PointerVisible            int32
	TotalMetadataBufferSize   uint32
	PointerShapeBufferSize    uint32
}

// ptrSize is the size of a pointer on this platform (4 or 8 bytes).
var ptrSize = unsafe.Sizeof(uintptr(0))

// comMethod returns the function pointer for the nth vtable method on a COM object.
func comMethod(obj uintptr, index int) uintptr {
	vtbl := *(*uintptr)(unsafe.Pointer(obj))
	return *(*uintptr)(unsafe.Pointer(vtbl + uintptr(index)*ptrSize))
}

// comRelease calls IUnknown::Release on a COM object.
func comRelease(obj uintptr) {
	if obj != 0 {
		syscall.SyscallN(comMethod(obj, 2), obj)
	}
}

// comQueryInterface calls IUnknown::QueryInterface.
func comQueryInterface(obj uintptr, iid *comGUID) (uintptr, error) {
	var result uintptr
	hr, _, _ := syscall.SyscallN(comMethod(obj, 0), obj,
		uintptr(unsafe.Pointer(iid)),
		uintptr(unsafe.Pointer(&result)))
	if hr != 0 {
		return 0, fmt.Errorf("QueryInterface failed: 0x%08X", hr)
	}
	return result, nil
}

// DXGICapture captures the primary screen using DXGI Desktop Duplication.
// This is significantly faster than GDI BitBlt (typically <5ms vs 30ms+).
// Requires Windows 8 or later. Falls back to GDI if DXGI is unavailable.
type DXGICapture struct {
	mu      sync.Mutex
	device  uintptr // ID3D11Device*
	ctx     uintptr // ID3D11DeviceContext*
	dupl    uintptr // IDXGIOutputDuplication*
	staging uintptr // ID3D11Texture2D* (staging, CPU-readable)
	width   uint16
	height  uint16
	stride  int
	pixels  []byte // persistent pixel buffer (avoids per-frame allocation)

	gdi *GDICapture // fallback if DXGI unavailable
}

func (d *DXGICapture) Init() error {
	if err := d.initDXGI(); err != nil {
		log.Printf("DXGI Desktop Duplication unavailable (%v), falling back to GDI BitBlt", err)
		d.gdi = &GDICapture{}
		return d.gdi.Init()
	}
	return nil
}

func (d *DXGICapture) initDXGI() error {
	// Check if d3d11.dll is available
	if err := procD3D11CreateDevice.Find(); err != nil {
		return fmt.Errorf("d3d11.dll not available: %w", err)
	}

	// 1. Create D3D11 device with default hardware adapter
	var device, ctx uintptr
	hr, _, _ := procD3D11CreateDevice.Call(
		0,                                    // pAdapter (NULL = default)
		uintptr(d3dDriverTypeHardware),       // DriverType
		0,                                    // Software module
		0,                                    // Flags
		0,                                    // pFeatureLevels (NULL = default)
		0,                                    // FeatureLevels count
		uintptr(d3d11SDKVersion),             // SDKVersion
		uintptr(unsafe.Pointer(&device)),     // ppDevice
		0,                                    // pFeatureLevel (don't need)
		uintptr(unsafe.Pointer(&ctx)),        // ppImmediateContext
	)
	if hr != 0 {
		return fmt.Errorf("D3D11CreateDevice: 0x%08X", hr)
	}

	// 2. device → IDXGIDevice → GetAdapter → EnumOutputs(0) → IDXGIOutput1 → DuplicateOutput
	dxgiDev, err := comQueryInterface(device, &iidIDXGIDevice)
	if err != nil {
		comRelease(ctx)
		comRelease(device)
		return fmt.Errorf("QueryInterface(IDXGIDevice): %w", err)
	}
	defer comRelease(dxgiDev)

	var adapter uintptr
	hr, _, _ = syscall.SyscallN(comMethod(dxgiDev, methDXGIDeviceGetAdapter),
		dxgiDev, uintptr(unsafe.Pointer(&adapter)))
	if hr != 0 {
		comRelease(ctx)
		comRelease(device)
		return fmt.Errorf("IDXGIDevice::GetAdapter: 0x%08X", hr)
	}
	defer comRelease(adapter)

	var output uintptr
	hr, _, _ = syscall.SyscallN(comMethod(adapter, methAdapterEnumOutputs),
		adapter, 0, uintptr(unsafe.Pointer(&output)))
	if hr != 0 {
		comRelease(ctx)
		comRelease(device)
		return fmt.Errorf("IDXGIAdapter::EnumOutputs(0): 0x%08X", hr)
	}
	defer comRelease(output)

	output1, err := comQueryInterface(output, &iidIDXGIOutput1)
	if err != nil {
		comRelease(ctx)
		comRelease(device)
		return fmt.Errorf("QueryInterface(IDXGIOutput1): %w", err)
	}
	defer comRelease(output1)

	var dupl uintptr
	hr, _, _ = syscall.SyscallN(comMethod(output1, methOutput1DuplicateOutput),
		output1, device, uintptr(unsafe.Pointer(&dupl)))
	if hr != 0 {
		comRelease(ctx)
		comRelease(device)
		return fmt.Errorf("IDXGIOutput1::DuplicateOutput: 0x%08X", hr)
	}

	// 3. Get screen dimensions and create staging texture
	w, _, _ := procGetSystemMetrics.Call(smCXScreen)
	h, _, _ := procGetSystemMetrics.Call(smCYScreen)
	if w == 0 || h == 0 {
		comRelease(dupl)
		comRelease(ctx)
		comRelease(device)
		return fmt.Errorf("GetSystemMetrics returned zero screen size")
	}

	desc := d3d11Texture2DDesc{
		Width:          uint32(w),
		Height:         uint32(h),
		MipLevels:      1,
		ArraySize:      1,
		Format:         dxgiFormatB8G8R8A8UNorm,
		SampleDesc:     dxgiSampleDesc{Count: 1, Quality: 0},
		Usage:          d3d11UsageStaging,
		CPUAccessFlags: d3d11CPUAccessRead,
	}
	var staging uintptr
	hr, _, _ = syscall.SyscallN(comMethod(device, methDeviceCreateTexture2D),
		device,
		uintptr(unsafe.Pointer(&desc)),
		0, // pInitialData
		uintptr(unsafe.Pointer(&staging)))
	if hr != 0 {
		comRelease(dupl)
		comRelease(ctx)
		comRelease(device)
		return fmt.Errorf("ID3D11Device::CreateTexture2D(staging): 0x%08X", hr)
	}

	d.device = device
	d.ctx = ctx
	d.dupl = dupl
	d.staging = staging
	d.width = uint16(w)
	d.height = uint16(h)
	d.stride = int(w) * 4
	d.pixels = make([]byte, int(w)*int(h)*4)

	// Fill with black initially (alpha=255)
	for i := 3; i < len(d.pixels); i += 4 {
		d.pixels[i] = 255
	}

	log.Printf("screen capture: using DXGI Desktop Duplication (Windows 8+)")
	return nil
}

func (d *DXGICapture) Bounds() (uint16, uint16) {
	if d.gdi != nil {
		return d.gdi.Bounds()
	}
	return d.width, d.height
}

func (d *DXGICapture) Capture() ([]byte, int, error) {
	if d.gdi != nil {
		return d.gdi.Capture()
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.dupl == 0 {
		return nil, 0, fmt.Errorf("DXGI capture not initialized")
	}

	// Acquire the next desktop frame (timeout 0 = non-blocking).
	// If no frame is available (screen unchanged), return the last captured pixels.
	var frameInfo dxgiOutduplFrameInfo
	var desktopResource uintptr
	hr, _, _ := syscall.SyscallN(comMethod(d.dupl, methDuplAcquireNextFrame),
		d.dupl,
		0, // timeout ms (0 = non-blocking)
		uintptr(unsafe.Pointer(&frameInfo)),
		uintptr(unsafe.Pointer(&desktopResource)))

	if hr == uintptr(dxgiErrorWaitTimeout) {
		// No new frame — return last captured pixels
		return d.pixels, d.stride, nil
	}

	if hr == uintptr(dxgiErrorAccessLost) {
		// Desktop switched (UAC, lock screen, etc.) — try to recreate
		d.recreateDuplication()
		return d.pixels, d.stride, nil
	}

	if hr != 0 {
		return nil, 0, fmt.Errorf("AcquireNextFrame: 0x%08X", hr)
	}

	// Get the desktop texture from the resource
	desktopTex, err := comQueryInterface(desktopResource, &iidID3D11Texture2D)
	comRelease(desktopResource)
	if err != nil {
		syscall.SyscallN(comMethod(d.dupl, methDuplReleaseFrame), d.dupl)
		return nil, 0, fmt.Errorf("QueryInterface(ID3D11Texture2D): %w", err)
	}

	// Copy desktop texture to our staging texture (GPU→GPU, fast)
	syscall.SyscallN(comMethod(d.ctx, methCtxCopyResource),
		d.ctx, d.staging, desktopTex)
	comRelease(desktopTex)

	// Map the staging texture to CPU memory
	var mapped d3d11MappedSubresource
	hr, _, _ = syscall.SyscallN(comMethod(d.ctx, methCtxMap),
		d.ctx,
		d.staging,
		0,                    // Subresource
		uintptr(d3d11MapRead), // MapType
		0,                    // MapFlags
		uintptr(unsafe.Pointer(&mapped)))

	if hr != 0 {
		syscall.SyscallN(comMethod(d.dupl, methDuplReleaseFrame), d.dupl)
		return nil, 0, fmt.Errorf("Map(staging): 0x%08X", hr)
	}

	// Copy pixels from mapped memory to our persistent buffer.
	// Handle row pitch (may differ from width*4 due to GPU alignment).
	w := int(d.width)
	h := int(d.height)
	rowBytes := w * 4
	srcPitch := int(mapped.RowPitch)

	if srcPitch == rowBytes {
		// Fast path: no padding, single memcpy
		src := unsafe.Slice((*byte)(unsafe.Pointer(mapped.PData)), h*srcPitch)
		copy(d.pixels, src)
	} else {
		// Row-by-row copy (GPU pitch != display width)
		for row := 0; row < h; row++ {
			srcRow := unsafe.Slice((*byte)(unsafe.Pointer(mapped.PData+uintptr(row*srcPitch))), rowBytes)
			dstOff := row * rowBytes
			copy(d.pixels[dstOff:dstOff+rowBytes], srcRow)
		}
	}

	// Unmap and release
	syscall.SyscallN(comMethod(d.ctx, methCtxUnmap), d.ctx, d.staging, 0)
	syscall.SyscallN(comMethod(d.dupl, methDuplReleaseFrame), d.dupl)

	return d.pixels, d.stride, nil
}

// recreateDuplication handles DXGI_ERROR_ACCESS_LOST by releasing and
// re-creating the output duplication. This happens on desktop switches
// (UAC, lock screen, Ctrl+Alt+Del).
func (d *DXGICapture) recreateDuplication() {
	if d.dupl != 0 {
		comRelease(d.dupl)
		d.dupl = 0
	}

	// Re-traverse: device → IDXGIDevice → adapter → output → output1 → DuplicateOutput
	dxgiDev, err := comQueryInterface(d.device, &iidIDXGIDevice)
	if err != nil {
		log.Printf("DXGI recreate: QueryInterface(IDXGIDevice) failed: %v", err)
		return
	}
	defer comRelease(dxgiDev)

	var adapter uintptr
	hr, _, _ := syscall.SyscallN(comMethod(dxgiDev, methDXGIDeviceGetAdapter),
		dxgiDev, uintptr(unsafe.Pointer(&adapter)))
	if hr != 0 {
		log.Printf("DXGI recreate: GetAdapter failed: 0x%08X", hr)
		return
	}
	defer comRelease(adapter)

	var output uintptr
	hr, _, _ = syscall.SyscallN(comMethod(adapter, methAdapterEnumOutputs),
		adapter, 0, uintptr(unsafe.Pointer(&output)))
	if hr != 0 {
		log.Printf("DXGI recreate: EnumOutputs failed: 0x%08X", hr)
		return
	}
	defer comRelease(output)

	output1, err := comQueryInterface(output, &iidIDXGIOutput1)
	if err != nil {
		log.Printf("DXGI recreate: QueryInterface(IDXGIOutput1) failed: %v", err)
		return
	}
	defer comRelease(output1)

	var dupl uintptr
	hr, _, _ = syscall.SyscallN(comMethod(output1, methOutput1DuplicateOutput),
		output1, d.device, uintptr(unsafe.Pointer(&dupl)))
	if hr != 0 {
		log.Printf("DXGI recreate: DuplicateOutput failed: 0x%08X", hr)
		return
	}

	d.dupl = dupl
	log.Printf("DXGI output duplication re-created successfully")
}

func (d *DXGICapture) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.gdi != nil {
		return d.gdi.Close()
	}

	if d.staging != 0 {
		comRelease(d.staging)
		d.staging = 0
	}
	if d.dupl != 0 {
		comRelease(d.dupl)
		d.dupl = 0
	}
	if d.ctx != 0 {
		comRelease(d.ctx)
		d.ctx = 0
	}
	if d.device != 0 {
		comRelease(d.device)
		d.device = 0
	}
	d.pixels = nil
	return nil
}
