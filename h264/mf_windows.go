//go:build windows

package h264

import (
	"fmt"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"
)

// ---- DLL and proc handles ----

var (
	mfplatDLL = syscall.NewLazyDLL("mfplat.dll")
	ole32DLL  = syscall.NewLazyDLL("ole32.dll")

	procCoInitializeEx      = ole32DLL.NewProc("CoInitializeEx")
	procCoCreateInstance     = ole32DLL.NewProc("CoCreateInstance")
	procMFStartup           = mfplatDLL.NewProc("MFStartup")
	procMFShutdown          = mfplatDLL.NewProc("MFShutdown")
	procMFCreateMediaType   = mfplatDLL.NewProc("MFCreateMediaType")
	procMFCreateSample      = mfplatDLL.NewProc("MFCreateSample")
	procMFCreateMemoryBuffer = mfplatDLL.NewProc("MFCreateMemoryBuffer")
)

// ---- COM helpers (same pattern as capture_windows_dxgi.go) ----

var ptrSize = unsafe.Sizeof(uintptr(0))

type comGUID struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

func mfComMethod(obj unsafe.Pointer, index int) uintptr {
	vtbl := *(*unsafe.Pointer)(obj)
	return *(*uintptr)(unsafe.Add(vtbl, uintptr(index)*ptrSize))
}

func mfComRelease(obj unsafe.Pointer) {
	if obj != nil {
		syscall.SyscallN(mfComMethod(obj, 2), uintptr(obj))
	}
}

func mfComQueryInterface(obj unsafe.Pointer, iid *comGUID) (unsafe.Pointer, error) {
	var result unsafe.Pointer
	hr, _, _ := syscall.SyscallN(mfComMethod(obj, 0), uintptr(obj),
		uintptr(unsafe.Pointer(iid)),
		uintptr(unsafe.Pointer(&result)))
	if hr != 0 {
		return nil, fmt.Errorf("QueryInterface: 0x%08X", hr)
	}
	return result, nil
}

// ---- GUIDs ----

var (
	// CLSID_CMSH264EncoderMFT
	clsidCMSH264Encoder = comGUID{0x6ca50344, 0x051a, 0x4ded, [8]byte{0x97, 0x79, 0xa4, 0x33, 0x05, 0x16, 0x5e, 0x35}}

	// IID_IMFTransform
	iidIMFTransform = comGUID{0xbf94c121, 0x5b05, 0x4e6f, [8]byte{0x80, 0x00, 0xba, 0x59, 0x89, 0x61, 0x41, 0x4d}}

	// IID_ICodecAPI
	iidICodecAPI = comGUID{0x901db4c7, 0x31ce, 0x41a2, [8]byte{0x85, 0xdc, 0x8f, 0xa0, 0xbf, 0x41, 0xb8, 0xda}}

	// Media type GUIDs
	mfMediaTypeVideo = comGUID{0x73646976, 0x0000, 0x0010, [8]byte{0x80, 0x00, 0x00, 0xaa, 0x00, 0x38, 0x9b, 0x71}}
	mfVideoFormatH264 = comGUID{0x34363248, 0x0000, 0x0010, [8]byte{0x80, 0x00, 0x00, 0xaa, 0x00, 0x38, 0x9b, 0x71}}
	mfVideoFormatNV12 = comGUID{0x3231564e, 0x0000, 0x0010, [8]byte{0x80, 0x00, 0x00, 0xaa, 0x00, 0x38, 0x9b, 0x71}}

	// Attribute GUIDs
	mfMTMajorType     = comGUID{0x48eba18e, 0xf8c9, 0x4687, [8]byte{0xbf, 0x11, 0x0a, 0x74, 0xc9, 0xf9, 0x6a, 0x8f}}
	mfMTSubtype       = comGUID{0xf7e34c9a, 0x42e8, 0x4714, [8]byte{0xb7, 0x4b, 0xcb, 0x29, 0xd7, 0x2c, 0x35, 0xe5}}
	mfMTAvgBitrate    = comGUID{0x20332624, 0xfb0d, 0x4d9e, [8]byte{0xbd, 0x0d, 0xcb, 0xf6, 0x78, 0x6c, 0x10, 0x2e}}
	mfMTInterlaceMode = comGUID{0xe2724bb8, 0xe676, 0x4806, [8]byte{0xb4, 0xb2, 0xa8, 0xd6, 0xef, 0xb4, 0x4c, 0xcd}}
	mfMTFrameSize     = comGUID{0x1652c33d, 0xd6b2, 0x4012, [8]byte{0xb8, 0x34, 0x72, 0x03, 0x08, 0x49, 0xa3, 0x7d}}
	mfMTFrameRate     = comGUID{0xc459a2e8, 0x3d2c, 0x4e44, [8]byte{0xb1, 0x32, 0xfe, 0xe5, 0x15, 0x6c, 0x7b, 0xb0}}

	// MF_MT_MPEG2_PROFILE
	mfMTMPEG2Profile = comGUID{0xad76a80b, 0x2d5c, 0x4e0b, [8]byte{0xb3, 0x75, 0x64, 0xe5, 0x20, 0x13, 0x70, 0x36}}

	// Low latency attribute
	mfLowLatency = comGUID{0x9c27891a, 0xed7a, 0x40e1, [8]byte{0x88, 0xe8, 0xb2, 0x27, 0x27, 0xa0, 0x24, 0xee}}

	// CodecAPI GUIDs
	codecapiAVEncCommonRateControlMode = comGUID{0x1c0608e9, 0x370c, 0x4710, [8]byte{0x8a, 0x58, 0xcb, 0x61, 0x81, 0xc4, 0x24, 0x23}}
	codecapiAVEncH264CABACEnable       = comGUID{0xee6cad62, 0xd305, 0x4248, [8]byte{0xa5, 0x0e, 0xe1, 0xb2, 0x55, 0xf7, 0xca, 0xf8}}
	codecapiAVEncVideoForceKeyFrame    = comGUID{0x398c1b98, 0x8353, 0x475a, [8]byte{0x9e, 0xf2, 0x8f, 0x26, 0x5d, 0x26, 0x03, 0x45}}
)

// ---- vtable indices ----

const (
	// IMFAttributes (base for IMFMediaType, IMFSample)
	methAttrGetUINT32 = 7
	methAttrSetUINT32 = 21
	methAttrSetUINT64 = 22
	methAttrSetGUID   = 24

	// IMFTransform
	methTransformGetOutputStreamInfo = 7
	methTransformGetAttributes       = 8
	methTransformSetInputType        = 15
	methTransformSetOutputType       = 16
	methTransformProcessMessage      = 23
	methTransformProcessInput        = 24
	methTransformProcessOutput       = 25

	// IMFSample
	methSampleSetSampleTime     = 36
	methSampleSetSampleDuration = 38
	methSampleAddBuffer         = 42

	// IMFMediaBuffer
	methBufferLock             = 3
	methBufferUnlock           = 4
	methBufferSetCurrentLength = 6

	// ICodecAPI (IUnknown=3 + IsSupported=3, IsModifiable=4, GetParameterRange=5,
	// GetParameterValues=6, GetDefaultValue=7, GetValue=8, SetValue=9)
	methCodecAPISetValue = 9

	// Constants
	coinitMultithreaded = 0x0
	clsctxInprocServer  = 0x1
	mfVersion           = 0x00020070

	mftMessageNotifyBeginStreaming = 0x10000000
	mftMessageNotifyStartOfStream = 0x10000003
	mftMessageCommandFlush        = 0x20000000

	mfVideoInterlaceProgressive = 2

	// MF_E_TRANSFORM_NEED_MORE_INPUT
	mfETransformNeedMoreInput = 0xC00D6D72

	// eAVEncCommonRateControlMode_CBR = 0 (not 2, which is UnconstrainedVBR)
	eAVEncCommonRateControlModeCBR = 0

	// eAVEncH264VProfile_Base (Baseline profile = 66)
	eAVEncH264VProfileBase = 66

	// MFT_OUTPUT_STREAM_PROVIDES_SAMPLES
	mftOutputStreamProvidesSamples = 0x100

	// VT_UI4 for VARIANT
	vtUI4 = 19
)

// ---- Structs matching C layout (64-bit) ----

type mftOutputDataBuffer struct {
	StreamID uint32
	_pad1    uint32
	PSample  unsafe.Pointer
	Status   uint32
	_pad2    uint32
	PEvents  unsafe.Pointer
}

type mftOutputStreamInfo struct {
	Flags        uint32
	Size         uint32
	Alignment    uint32
}

// ---- MF lifecycle ----

var (
	mfInitOnce sync.Once
	mfInitErr  error
	mfRefCount int32
)

func mfEnsureInit() error {
	mfInitOnce.Do(func() {
		hr, _, _ := procCoInitializeEx.Call(0, uintptr(coinitMultithreaded))
		// S_OK=0 or S_FALSE=1 (already initialized) are both OK.
		if hr != 0 && hr != 1 {
			mfInitErr = fmt.Errorf("CoInitializeEx: 0x%08X", hr)
			return
		}
		hr, _, _ = procMFStartup.Call(uintptr(mfVersion), 0)
		if hr != 0 {
			mfInitErr = fmt.Errorf("MFStartup: 0x%08X", hr)
			return
		}
	})
	if mfInitErr == nil {
		atomic.AddInt32(&mfRefCount, 1)
	}
	return mfInitErr
}

func mfRelease() {
	if atomic.AddInt32(&mfRefCount, -1) <= 0 {
		procMFShutdown.Call()
	}
}

// ---- mfBackend ----

type mfBackend struct {
	transform unsafe.Pointer // IMFTransform*
	codecAPI  unsafe.Pointer // ICodecAPI* (for forcing IDR)
	width     int
	height    int
	sampleDur int64 // 100-ns units per frame
	pts       int64 // presentation timestamp counter
	mftProvidesSamples bool
	outputBufSize      int // minimum output buffer size from GetOutputStreamInfo
}

func newBackend(width, height int) (h264Backend, error) {
	if err := mfEnsureInit(); err != nil {
		return nil, err
	}

	b := &mfBackend{
		width:     width,
		height:    height,
		sampleDur: 333333, // 30fps in 100-ns units
	}

	if err := b.init(); err != nil {
		mfRelease()
		return nil, err
	}
	return b, nil
}

func (b *mfBackend) init() error {
	// Create the H.264 encoder MFT.
	var transform unsafe.Pointer
	hr, _, _ := procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsidCMSH264Encoder)),
		0,
		uintptr(clsctxInprocServer),
		uintptr(unsafe.Pointer(&iidIMFTransform)),
		uintptr(unsafe.Pointer(&transform)))
	if hr != 0 {
		return fmt.Errorf("CoCreateInstance(H264 MFT): 0x%08X", hr)
	}
	b.transform = transform

	// Get ICodecAPI for forcing IDR.
	codecAPI, err := mfComQueryInterface(transform, &iidICodecAPI)
	if err == nil {
		b.codecAPI = codecAPI
	}
	// Not fatal if ICodecAPI is unavailable.

	// Configure output type (H.264).
	if err := b.setOutputType(); err != nil {
		b.Close()
		return fmt.Errorf("set output type: %w", err)
	}

	// Configure input type (NV12).
	if err := b.setInputType(); err != nil {
		b.Close()
		return fmt.Errorf("set input type: %w", err)
	}

	// Set low-latency on transform attributes.
	b.setLowLatency()

	// Set Baseline profile (disable CABAC) via CodecAPI.
	b.setCodecAPIUINT32(&codecapiAVEncH264CABACEnable, 0)
	b.setCodecAPIUINT32(&codecapiAVEncCommonRateControlMode, eAVEncCommonRateControlModeCBR)

	// Query output stream info to check if MFT provides samples and get buffer size.
	var streamInfo mftOutputStreamInfo
	hr2, _, _ := syscall.SyscallN(mfComMethod(b.transform, methTransformGetOutputStreamInfo),
		uintptr(b.transform), 0, uintptr(unsafe.Pointer(&streamInfo)))
	if hr2 == 0 {
		b.mftProvidesSamples = (streamInfo.Flags & mftOutputStreamProvidesSamples) != 0
		if streamInfo.Size > 0 {
			b.outputBufSize = int(streamInfo.Size)
		}
	}

	// Begin streaming.
	syscall.SyscallN(mfComMethod(b.transform, methTransformProcessMessage),
		uintptr(b.transform), uintptr(mftMessageNotifyBeginStreaming), 0)
	syscall.SyscallN(mfComMethod(b.transform, methTransformProcessMessage),
		uintptr(b.transform), uintptr(mftMessageNotifyStartOfStream), 0)

	return nil
}

func (b *mfBackend) setOutputType() error {
	var mediaType unsafe.Pointer
	hr, _, _ := procMFCreateMediaType.Call(uintptr(unsafe.Pointer(&mediaType)))
	if hr != 0 {
		return fmt.Errorf("MFCreateMediaType: 0x%08X", hr)
	}
	defer mfComRelease(mediaType)

	setGUID(mediaType, &mfMTMajorType, &mfMediaTypeVideo)
	setGUID(mediaType, &mfMTSubtype, &mfVideoFormatH264)
	setUINT32(mediaType, &mfMTAvgBitrate, 5000000) // 5 Mbps
	setUINT32(mediaType, &mfMTInterlaceMode, mfVideoInterlaceProgressive)
	setUINT64(mediaType, &mfMTFrameSize, pack64(uint32(b.width), uint32(b.height)))
	setUINT64(mediaType, &mfMTFrameRate, pack64(30, 1))
	setUINT32(mediaType, &mfMTMPEG2Profile, eAVEncH264VProfileBase) // Baseline profile

	hr2, _, _ := syscall.SyscallN(mfComMethod(b.transform, methTransformSetOutputType),
		uintptr(b.transform), 0, uintptr(mediaType), 0)
	if hr2 != 0 {
		return fmt.Errorf("SetOutputType: 0x%08X", hr2)
	}
	return nil
}

func (b *mfBackend) setInputType() error {
	var mediaType unsafe.Pointer
	hr, _, _ := procMFCreateMediaType.Call(uintptr(unsafe.Pointer(&mediaType)))
	if hr != 0 {
		return fmt.Errorf("MFCreateMediaType: 0x%08X", hr)
	}
	defer mfComRelease(mediaType)

	setGUID(mediaType, &mfMTMajorType, &mfMediaTypeVideo)
	setGUID(mediaType, &mfMTSubtype, &mfVideoFormatNV12)
	setUINT32(mediaType, &mfMTInterlaceMode, mfVideoInterlaceProgressive)
	setUINT64(mediaType, &mfMTFrameSize, pack64(uint32(b.width), uint32(b.height)))
	setUINT64(mediaType, &mfMTFrameRate, pack64(30, 1))

	hr2, _, _ := syscall.SyscallN(mfComMethod(b.transform, methTransformSetInputType),
		uintptr(b.transform), 0, uintptr(mediaType), 0)
	if hr2 != 0 {
		return fmt.Errorf("SetInputType: 0x%08X", hr2)
	}
	return nil
}

func (b *mfBackend) setLowLatency() {
	var attrs unsafe.Pointer
	hr, _, _ := syscall.SyscallN(mfComMethod(b.transform, methTransformGetAttributes),
		uintptr(b.transform), uintptr(unsafe.Pointer(&attrs)))
	if hr != 0 || attrs == nil {
		return
	}
	defer mfComRelease(attrs)
	setUINT32(attrs, &mfLowLatency, 1)
}

func (b *mfBackend) setCodecAPIUINT32(guid *comGUID, val uint32) {
	if b.codecAPI == nil {
		return
	}
	// VARIANT: vt(2) + padding(6) + value(8) = 16 bytes on 64-bit
	var variant [24]byte
	*(*uint16)(unsafe.Pointer(&variant[0])) = vtUI4
	*(*uint32)(unsafe.Pointer(&variant[8])) = val
	syscall.SyscallN(mfComMethod(b.codecAPI, methCodecAPISetValue),
		uintptr(b.codecAPI),
		uintptr(unsafe.Pointer(guid)),
		uintptr(unsafe.Pointer(&variant[0])))
}

func (b *mfBackend) Encode(nv12 []byte, forceIDR bool) ([]byte, bool, error) {
	// Force IDR if requested.
	if forceIDR && b.codecAPI != nil {
		b.setCodecAPIUINT32(&codecapiAVEncVideoForceKeyFrame, 1)
	}

	// Create IMFMediaBuffer from NV12 data.
	nv12Len := len(nv12)
	var buffer unsafe.Pointer
	hr, _, _ := procMFCreateMemoryBuffer.Call(uintptr(nv12Len), uintptr(unsafe.Pointer(&buffer)))
	if hr != 0 {
		return nil, false, fmt.Errorf("MFCreateMemoryBuffer: 0x%08X", hr)
	}
	defer mfComRelease(buffer)

	// Lock, copy data, unlock.
	var pbuf unsafe.Pointer
	var maxLen, curLen uint32
	syscall.SyscallN(mfComMethod(buffer, methBufferLock),
		uintptr(buffer),
		uintptr(unsafe.Pointer(&pbuf)),
		uintptr(unsafe.Pointer(&maxLen)),
		uintptr(unsafe.Pointer(&curLen)))
	copy(unsafe.Slice((*byte)(pbuf), nv12Len), nv12)
	syscall.SyscallN(mfComMethod(buffer, methBufferUnlock), uintptr(buffer))
	syscall.SyscallN(mfComMethod(buffer, methBufferSetCurrentLength),
		uintptr(buffer), uintptr(nv12Len))

	// Create IMFSample.
	var sample unsafe.Pointer
	hr, _, _ = procMFCreateSample.Call(uintptr(unsafe.Pointer(&sample)))
	if hr != 0 {
		return nil, false, fmt.Errorf("MFCreateSample: 0x%08X", hr)
	}
	defer mfComRelease(sample)

	// Add buffer to sample.
	syscall.SyscallN(mfComMethod(sample, methSampleAddBuffer),
		uintptr(sample), uintptr(buffer))

	// Set sample time and duration.
	syscall.SyscallN(mfComMethod(sample, methSampleSetSampleTime),
		uintptr(sample), uintptr(b.pts))
	syscall.SyscallN(mfComMethod(sample, methSampleSetSampleDuration),
		uintptr(sample), uintptr(b.sampleDur))
	b.pts += b.sampleDur

	// ProcessInput.
	hr2, _, _ := syscall.SyscallN(mfComMethod(b.transform, methTransformProcessInput),
		uintptr(b.transform), 0, uintptr(sample), 0)
	if hr2 != 0 {
		return nil, false, fmt.Errorf("ProcessInput: 0x%08X", hr2)
	}

	// ProcessOutput.
	return b.drainOutput()
}

func (b *mfBackend) drainOutput() ([]byte, bool, error) {
	var outData mftOutputDataBuffer
	var outSample unsafe.Pointer

	// If MFT doesn't provide samples, we must create one.
	if !b.mftProvidesSamples {
		var s unsafe.Pointer
		hr, _, _ := procMFCreateSample.Call(uintptr(unsafe.Pointer(&s)))
		if hr != 0 {
			return nil, false, fmt.Errorf("MFCreateSample (output): 0x%08X", hr)
		}
		// Create a buffer large enough for encoded data.
		bufSize := 1024 * 1024 // 1 MB default
		if b.outputBufSize > 0 {
			bufSize = b.outputBufSize
		}
		var buf unsafe.Pointer
		hr2, _, _ := procMFCreateMemoryBuffer.Call(uintptr(bufSize), uintptr(unsafe.Pointer(&buf)))
		if hr2 != 0 {
			mfComRelease(s)
			return nil, false, fmt.Errorf("MFCreateMemoryBuffer (output): 0x%08X", hr2)
		}
		hr3, _, _ := syscall.SyscallN(mfComMethod(s, methSampleAddBuffer), uintptr(s), uintptr(buf))
		mfComRelease(buf)
		if hr3 != 0 {
			mfComRelease(s)
			return nil, false, fmt.Errorf("AddBuffer (output): 0x%08X", hr3)
		}
		outSample = s
		outData.PSample = s
	}

	var status uint32
	hr, _, _ := syscall.SyscallN(mfComMethod(b.transform, methTransformProcessOutput),
		uintptr(b.transform), 0, 1,
		uintptr(unsafe.Pointer(&outData)),
		uintptr(unsafe.Pointer(&status)))

	if hr == uintptr(mfETransformNeedMoreInput) {
		if outSample != nil {
			mfComRelease(outSample)
		}
		return nil, false, nil // No output yet (latency).
	}
	if hr != 0 {
		if outSample != nil {
			mfComRelease(outSample)
		}
		return nil, false, fmt.Errorf("ProcessOutput: 0x%08X", hr)
	}

	resultSample := outData.PSample
	if resultSample == nil {
		return nil, false, nil
	}
	if b.mftProvidesSamples {
		defer mfComRelease(resultSample)
	} else {
		defer mfComRelease(outSample)
	}

	// Clean up events if any.
	if outData.PEvents != nil {
		mfComRelease(outData.PEvents)
	}

	// Extract NAL data from the output sample's buffer.
	// ConvertToContiguousBuffer is at IMFSample vtable index 41.
	var outBuf unsafe.Pointer
	hr2, _, _ := syscall.SyscallN(mfComMethod(resultSample, 41),
		uintptr(resultSample), uintptr(unsafe.Pointer(&outBuf)))
	if hr2 != 0 {
		return nil, false, fmt.Errorf("ConvertToContiguousBuffer: 0x%08X", hr2)
	}
	defer mfComRelease(outBuf)

	var bufPtr unsafe.Pointer
	var maxLen2, curLen2 uint32
	syscall.SyscallN(mfComMethod(outBuf, methBufferLock),
		uintptr(outBuf),
		uintptr(unsafe.Pointer(&bufPtr)),
		uintptr(unsafe.Pointer(&maxLen2)),
		uintptr(unsafe.Pointer(&curLen2)))

	nalData := make([]byte, curLen2)
	copy(nalData, unsafe.Slice((*byte)(bufPtr), curLen2))

	syscall.SyscallN(mfComMethod(outBuf, methBufferUnlock), uintptr(outBuf))

	// Detect keyframe: check if NAL data starts with IDR NAL unit.
	isKeyframe := false
	if len(nalData) > 4 {
		// Look for IDR NAL type (5) in Annex B stream.
		isKeyframe = isIDRFrame(nalData)
	}

	return nalData, isKeyframe, nil
}

func (b *mfBackend) Close() {
	if b.transform != nil {
		syscall.SyscallN(mfComMethod(b.transform, methTransformProcessMessage),
			uintptr(b.transform), uintptr(mftMessageCommandFlush), 0)
		mfComRelease(b.transform)
		b.transform = nil
	}
	if b.codecAPI != nil {
		mfComRelease(b.codecAPI)
		b.codecAPI = nil
	}
	mfRelease()
}

// ---- IMFAttributes helpers ----

func setGUID(attrs unsafe.Pointer, key, val *comGUID) {
	syscall.SyscallN(mfComMethod(attrs, methAttrSetGUID),
		uintptr(attrs),
		uintptr(unsafe.Pointer(key)),
		uintptr(unsafe.Pointer(val)))
}

func setUINT32(attrs unsafe.Pointer, key *comGUID, val uint32) {
	syscall.SyscallN(mfComMethod(attrs, methAttrSetUINT32),
		uintptr(attrs),
		uintptr(unsafe.Pointer(key)),
		uintptr(val))
}

func setUINT64(attrs unsafe.Pointer, key *comGUID, val uint64) {
	syscall.SyscallN(mfComMethod(attrs, methAttrSetUINT64),
		uintptr(attrs),
		uintptr(unsafe.Pointer(key)),
		uintptr(val))
}

func pack64(hi, lo uint32) uint64 {
	return uint64(hi)<<32 | uint64(lo)
}

// isIDRFrame scans Annex B NAL units for an IDR slice (NAL type 5).
func isIDRFrame(data []byte) bool {
	i := 0
	for i < len(data)-4 {
		// Find start code (0x00000001 or 0x000001).
		if data[i] == 0 && data[i+1] == 0 {
			nalStart := -1
			if data[i+2] == 1 {
				nalStart = i + 3
			} else if data[i+2] == 0 && i+3 < len(data) && data[i+3] == 1 {
				nalStart = i + 4
			}
			if nalStart >= 0 && nalStart < len(data) {
				nalType := data[nalStart] & 0x1F
				if nalType == 5 { // IDR
					return true
				}
			}
		}
		i++
	}
	return false
}
