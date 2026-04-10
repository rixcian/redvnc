package encodings

import (
	"math"
	"sync"
	"testing"
)

// Benchmark parameters: 1920×1080 animated gradient, 16-frame pool.
// Each benchmark reuses a single encoder instance across all b.N frames
// (warm/streaming mode) so persistent zlib dictionary state is exercised
// exactly as it would be in a live VNC session.
const (
	benchWidth     = 1920
	benchHeight    = 1080
	benchFramePool = 16
)

var (
	gradientOnce   sync.Once
	gradientFrames [][]byte
)

// getBenchFrames returns a lazily-initialized pool of benchFramePool gradient
// frames at benchWidth×benchHeight (BGRA). The same formula as
// example/server/main.go is used so the benchmark reflects real server output.
func getBenchFrames() [][]byte {
	gradientOnce.Do(func() {
		stride := benchWidth * 4
		gradientFrames = make([][]byte, benchFramePool)
		for f := 0; f < benchFramePool; f++ {
			phase := float64(f) * 0.05
			pixels := make([]byte, benchHeight*stride)
			for y := 0; y < benchHeight; y++ {
				for x := 0; x < benchWidth; x++ {
					off := y*stride + x*4
					r := uint8(127 + 127*math.Sin(phase+float64(x)*0.02))
					g := uint8(127 + 127*math.Sin(phase+float64(y)*0.02))
					b := uint8(127 + 127*math.Sin(phase+float64(x+y)*0.01))
					pixels[off+0] = b   // Blue  (BGRA layout)
					pixels[off+1] = g   // Green
					pixels[off+2] = r   // Red
					pixels[off+3] = 255 // Alpha
				}
			}
			gradientFrames[f] = pixels
		}
	})
	return gradientFrames
}

// BenchmarkRaw_FHD_Gradient measures Raw encoding at 1920×1080 with an
// animated gradient. Raw applies no compression, so it serves as a throughput
// ceiling and allocation baseline for the other encoders.
func BenchmarkRaw_FHD_Gradient(b *testing.B) {
	frames := getBenchFrames()
	enc := &Raw{}
	stride := benchWidth * 4
	inputBytes := int64(benchWidth * benchHeight * 4)

	b.SetBytes(inputBytes)
	b.ResetTimer()

	var totalOut int64
	for i := 0; i < b.N; i++ {
		frame := frames[i%benchFramePool]
		rect, err := enc.Encode(0, 0, benchWidth, benchHeight, frame, stride)
		if err != nil {
			b.Fatal(err)
		}
		totalOut += int64(len(rect.Data))
	}

	b.ReportMetric(float64(totalOut)/float64(b.N)/float64(inputBytes), "ratio")
}

// BenchmarkZlib_FHD_Gradient measures Zlib encoding at 1920×1080.
// The encoder is created once so the persistent zlib dictionary carries over
// across frames, matching real session behaviour.
func BenchmarkZlib_FHD_Gradient(b *testing.B) {
	frames := getBenchFrames()
	enc := &Zlib{}
	defer enc.Reset()
	stride := benchWidth * 4
	inputBytes := int64(benchWidth * benchHeight * 4)

	b.SetBytes(inputBytes)
	b.ResetTimer()

	var totalOut int64
	for i := 0; i < b.N; i++ {
		frame := frames[i%benchFramePool]
		rect, err := enc.Encode(0, 0, benchWidth, benchHeight, frame, stride)
		if err != nil {
			b.Fatal(err)
		}
		totalOut += int64(len(rect.Data))
	}

	b.ReportMetric(float64(totalOut)/float64(b.N)/float64(inputBytes), "ratio")
}

// BenchmarkTight_FHD_Gradient measures Tight encoding at 1920×1080 using
// EncodeMulti (the server's actual code path), which returns one rectangle per
// 64×64 tile (~510 tiles at FHD). The four persistent zlib streams carry over
// across frames.
func BenchmarkTight_FHD_Gradient(b *testing.B) {
	frames := getBenchFrames()
	enc := NewTight(75)
	defer enc.Reset()
	stride := benchWidth * 4
	inputBytes := int64(benchWidth * benchHeight * 4)

	b.SetBytes(inputBytes)
	b.ResetTimer()

	var totalOut int64
	for i := 0; i < b.N; i++ {
		frame := frames[i%benchFramePool]
		rects, err := enc.EncodeMulti(0, 0, benchWidth, benchHeight, frame, stride)
		if err != nil {
			b.Fatal(err)
		}
		for j := range rects {
			totalOut += int64(len(rects[j].Data))
		}
	}

	b.ReportMetric(float64(totalOut)/float64(b.N)/float64(inputBytes), "ratio")
}

// BenchmarkZRLE_FHD_Gradient measures ZRLE encoding at 1920×1080.
// The encoder is created once so the persistent zlib stream carries over
// across frames.
func BenchmarkZRLE_FHD_Gradient(b *testing.B) {
	frames := getBenchFrames()
	enc := &ZRLE{}
	defer enc.Reset()
	stride := benchWidth * 4
	inputBytes := int64(benchWidth * benchHeight * 4)

	b.SetBytes(inputBytes)
	b.ResetTimer()

	var totalOut int64
	for i := 0; i < b.N; i++ {
		frame := frames[i%benchFramePool]
		rect, err := enc.Encode(0, 0, benchWidth, benchHeight, frame, stride)
		if err != nil {
			b.Fatal(err)
		}
		totalOut += int64(len(rect.Data))
	}

	b.ReportMetric(float64(totalOut)/float64(b.N)/float64(inputBytes), "ratio")
}
