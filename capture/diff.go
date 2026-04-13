package capture

import (
	"bytes"
	"image"
)

const defaultTileSize = 64

// FrameDiffer detects changed screen regions by comparing consecutive frames
// at tile granularity (default 64x64, matching Tight encoding tile size).
//
// Usage:
//
//	d := NewFrameDiffer()
//	rects := d.Diff(pixels, width, height, stride)
//	// rects == nil   → first frame (no previous to compare)
//	// rects == []    → nothing changed
//	// rects == [..]  → only these tile-aligned rectangles changed
type FrameDiffer struct {
	prev     []byte
	prevW    uint16
	prevH    uint16
	tileSize int
}

// NewFrameDiffer creates a FrameDiffer with the default 64x64 tile size.
func NewFrameDiffer() *FrameDiffer {
	return &FrameDiffer{tileSize: defaultTileSize}
}

// Diff compares pixels against the previously stored frame and returns the
// list of tile-aligned rectangles that changed.
//
// Return values follow the same convention as DirtyRectCapture.LastDirtyRects:
//
//   - nil:  first frame or resolution changed (encode everything)
//   - []:   nothing changed (skip encoding)
//   - [..]: only these regions changed (encode selectively)
//
// The caller must not retain pixels — Diff copies what it needs internally.
func (d *FrameDiffer) Diff(pixels []byte, width, height uint16, stride int) []image.Rectangle {
	bpp := 4
	frameSize := int(height) * stride

	// First frame or resolution change: store and signal full-frame.
	if d.prev == nil || d.prevW != width || d.prevH != height {
		if len(d.prev) < frameSize {
			d.prev = make([]byte, frameSize)
		}
		d.prev = d.prev[:frameSize]
		copy(d.prev, pixels[:frameSize])
		d.prevW = width
		d.prevH = height
		return nil
	}

	w := int(width)
	h := int(height)
	ts := d.tileSize

	var dirty []image.Rectangle

	for tileY := 0; tileY < h; tileY += ts {
		tileH := min(ts, h-tileY)
		for tileX := 0; tileX < w; tileX += ts {
			tileW := min(ts, w-tileX)

			changed := false
			for row := 0; row < tileH; row++ {
				y := tileY + row
				off := y*stride + tileX*bpp
				end := off + tileW*bpp
				if !bytes.Equal(pixels[off:end], d.prev[off:end]) {
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
	copy(d.prev, pixels[:frameSize])

	if dirty == nil {
		// No changes — return empty (not nil) to signal "nothing changed".
		return []image.Rectangle{}
	}
	return dirty
}
