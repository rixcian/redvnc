package rfb

import "encoding/binary"

// ConvertPixels converts raw pixel data from the source pixel format to the
// destination pixel format. Both src and dst formats must be TrueColour.
// The pixel buffer is assumed to be tightly packed (stride = width * bytesPerPixel).
func ConvertPixels(dst, src PixelFormat, pixels []byte, width, height int) []byte {
	if dst == src {
		return pixels
	}

	srcBpp := int(src.BitsPerPixel) / 8
	dstBpp := int(dst.BitsPerPixel) / 8
	out := make([]byte, width*height*dstBpp)

	for i := 0; i < width*height; i++ {
		srcOff := i * srcBpp
		dstOff := i * dstBpp

		// Extract RGB from source pixel
		var srcPixel uint32
		switch srcBpp {
		case 1:
			srcPixel = uint32(pixels[srcOff])
		case 2:
			if src.BigEndian != 0 {
				srcPixel = uint32(binary.BigEndian.Uint16(pixels[srcOff:]))
			} else {
				srcPixel = uint32(binary.LittleEndian.Uint16(pixels[srcOff:]))
			}
		case 4:
			if src.BigEndian != 0 {
				srcPixel = binary.BigEndian.Uint32(pixels[srcOff:])
			} else {
				srcPixel = binary.LittleEndian.Uint32(pixels[srcOff:])
			}
		}

		r := (srcPixel >> src.RedShift) & uint32(src.RedMax)
		g := (srcPixel >> src.GreenShift) & uint32(src.GreenMax)
		b := (srcPixel >> src.BlueShift) & uint32(src.BlueMax)

		// Scale to destination range
		if src.RedMax != dst.RedMax {
			r = r * uint32(dst.RedMax) / uint32(src.RedMax)
		}
		if src.GreenMax != dst.GreenMax {
			g = g * uint32(dst.GreenMax) / uint32(src.GreenMax)
		}
		if src.BlueMax != dst.BlueMax {
			b = b * uint32(dst.BlueMax) / uint32(src.BlueMax)
		}

		// Pack into destination pixel
		dstPixel := (r << dst.RedShift) | (g << dst.GreenShift) | (b << dst.BlueShift)

		switch dstBpp {
		case 1:
			out[dstOff] = uint8(dstPixel)
		case 2:
			if dst.BigEndian != 0 {
				binary.BigEndian.PutUint16(out[dstOff:], uint16(dstPixel))
			} else {
				binary.LittleEndian.PutUint16(out[dstOff:], uint16(dstPixel))
			}
		case 4:
			if dst.BigEndian != 0 {
				binary.BigEndian.PutUint32(out[dstOff:], dstPixel)
			} else {
				binary.LittleEndian.PutUint32(out[dstOff:], dstPixel)
			}
		}
	}

	return out
}
