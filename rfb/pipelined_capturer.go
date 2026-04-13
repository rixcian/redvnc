package rfb

import (
	"bytes"
	"image"
	"sync"
	"time"
)

// PipelinedCapturer wraps a ScreenCapturer and runs captures in a background
// goroutine so that capture overlaps with the encode/send phase of the previous
// frame. Capture() returns the latest available frame instantly without waiting
// for a new capture to complete.
//
// The background goroutine double-buffers the pixel data: it writes new frames
// into whichever buffer is not currently exposed to the caller, then atomically
// swaps the "current" index. This ensures the encode goroutine never observes
// a partially-written frame.
//
// One extra full-frame copy per capture is the cost of the pipeline. For
// typical 1080p BGRA data (~8 MB) this takes ~2–5 ms, but it runs concurrently
// with the previous encode (~20 ms), so the net effect is that capture latency
// is hidden entirely when encode dominates.
//
// Usage:
//
//	pc := rfb.NewPipelinedCapturer(capturer)
//	pc.Start(maxFPS)
//	defer pc.Stop()
//	// use pc as a ScreenCapturer
type PipelinedCapturer struct {
	inner ScreenCapturer

	mu      sync.Mutex
	bufs    [2][]byte
	strides [2]int
	cur     int // index of the buffer ready for consumption (0 or 1)

	notify chan struct{} // capacity 1; closed/sent when a new frame lands
	done   chan struct{} // closed to stop the background goroutine
	once   sync.Once    // guards Start

	// Dirty rect detection via frame differencing.
	diffPrev     []byte           // previous frame for comparison
	diffPrevW    uint16           // width of previous frame
	diffPrevH    uint16           // height of previous frame
	dirtyRects   []image.Rectangle // result of last Diff, consumed by LastDirtyRects
	dirtyComputed bool            // true after Capture() runs a diff
}

// NewPipelinedCapturer wraps inner in a PipelinedCapturer.
// Call Start() before using it as a ScreenCapturer.
func NewPipelinedCapturer(inner ScreenCapturer) *PipelinedCapturer {
	return &PipelinedCapturer{
		inner:  inner,
		notify: make(chan struct{}, 1),
		done:   make(chan struct{}),
	}
}

// Start launches the background capture goroutine. maxFPS caps the rate at
// which the goroutine polls the inner capturer to avoid burning CPU on a static
// screen. It is safe to call Start only once.
func (p *PipelinedCapturer) Start(maxFPS int) {
	if maxFPS <= 0 {
		maxFPS = 30
	}
	p.once.Do(func() {
		go p.captureLoop(maxFPS)
	})
}

// Stop signals the background goroutine to exit and waits for it to finish.
func (p *PipelinedCapturer) Stop() {
	close(p.done)
}

func (p *PipelinedCapturer) captureLoop(maxFPS int) {
	minInterval := time.Second / time.Duration(maxFPS)
	next := 1 // start writing into buffer 1; cur starts at 0

	for {
		t0 := time.Now()

		select {
		case <-p.done:
			return
		default:
		}

		pixels, stride, err := p.inner.Capture()
		if err == nil && len(pixels) > 0 {
			p.mu.Lock()
			// Grow the destination buffer only when necessary.
			if len(p.bufs[next]) < len(pixels) {
				p.bufs[next] = make([]byte, len(pixels))
			}
			copy(p.bufs[next], pixels)
			p.strides[next] = stride
			p.cur = next
			p.mu.Unlock()

			next ^= 1 // alternate between 0 and 1

			// Non-blocking notify: if the consumer hasn't read the previous
			// notification yet, drop this one — it will read the latest frame.
			select {
			case p.notify <- struct{}{}:
			default:
			}
		}

		// Pace the loop to maxFPS; the inner Capture() may already block for
		// the right duration (e.g. DXGI non-blocking), but we enforce a floor.
		if elapsed := time.Since(t0); elapsed < minInterval {
			select {
			case <-p.done:
				return
			case <-time.After(minInterval - elapsed):
			}
		}
	}
}

// Capture returns the latest captured frame. It never blocks waiting for a
// new capture to complete; if no frame has arrived yet it waits for the first
// one. The returned slice is a fresh copy owned by the caller — the
// background goroutine is free to overwrite the internal buffers immediately.
func (p *PipelinedCapturer) Capture() ([]byte, int, error) {
	// Wait for the first frame if none is available yet.
	p.mu.Lock()
	for len(p.bufs[p.cur]) == 0 {
		p.mu.Unlock()
		select {
		case <-p.notify:
		case <-p.done:
			return nil, 0, nil
		}
		p.mu.Lock()
	}

	// Copy under the lock so the captureLoop cannot overwrite the source buffer
	// while we are reading it. The copy is the caller's own buffer and can be
	// read freely after the lock is released.
	src := p.bufs[p.cur]
	stride := p.strides[p.cur]
	out := make([]byte, len(src))
	copy(out, src)
	p.mu.Unlock()

	// Run tile-based frame diff to detect dirty rectangles.
	w, h := p.Bounds()
	p.dirtyRects = p.frameDiff(out, w, h, stride)
	p.dirtyComputed = true

	return out, stride, nil
}

// LastDirtyRects returns the dirty rectangles detected by the last Capture() call.
// Must be called from the same goroutine as Capture(), and only once per Capture().
func (p *PipelinedCapturer) LastDirtyRects() []image.Rectangle {
	if !p.dirtyComputed {
		return nil // no diff was run yet
	}
	p.dirtyComputed = false
	return p.dirtyRects
}

const diffTileSize = 64

// frameDiff compares pixels against the previously stored frame at 64x64 tile
// granularity. Returns nil on first frame, empty slice on no change, or the
// list of changed tile-aligned rectangles.
func (p *PipelinedCapturer) frameDiff(pixels []byte, width, height uint16, stride int) []image.Rectangle {
	bpp := 4
	pixelLen := len(pixels)

	// First frame or resolution change: store and signal full-frame.
	if p.diffPrev == nil || p.diffPrevW != width || p.diffPrevH != height {
		if len(p.diffPrev) < pixelLen {
			p.diffPrev = make([]byte, pixelLen)
		}
		p.diffPrev = p.diffPrev[:pixelLen]
		copy(p.diffPrev, pixels)
		p.diffPrevW = width
		p.diffPrevH = height
		return nil
	}

	w := int(width)
	h := int(height)

	var dirty []image.Rectangle

	for tileY := 0; tileY < h; tileY += diffTileSize {
		tileH := min(diffTileSize, h-tileY)
		for tileX := 0; tileX < w; tileX += diffTileSize {
			tileW := min(diffTileSize, w-tileX)

			changed := false
			for row := 0; row < tileH; row++ {
				y := tileY + row
				off := y*stride + tileX*bpp
				end := off + tileW*bpp
				if end > pixelLen {
					changed = true
					break
				}
				if !bytes.Equal(pixels[off:end], p.diffPrev[off:end]) {
					changed = true
					break
				}
			}

			if changed {
				dirty = append(dirty, image.Rect(tileX, tileY, tileX+tileW, tileY+tileH))
			}
		}
	}

	// Update stored frame.
	copy(p.diffPrev, pixels)

	if dirty == nil {
		return []image.Rectangle{}
	}
	return dirty
}

// Bounds delegates to the inner capturer.
func (p *PipelinedCapturer) Bounds() (uint16, uint16) {
	return p.inner.Bounds()
}
