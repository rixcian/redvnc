// Package rfb implements the Remote Framebuffer (RFB) protocol as defined in RFC 6143.
package rfb

import (
	"encoding/binary"
	"fmt"
	"io"
)

// Protocol versions.
const (
	VersionString3_3 = "RFB 003.003\n"
	VersionString3_7 = "RFB 003.007\n"
	VersionString3_8 = "RFB 003.008\n"
)

// Security types.
const (
	SecurityNone    uint8 = 1
	SecurityVNCAuth uint8 = 2
)

// Client-to-server message types.
const (
	MsgSetPixelFormat           uint8 = 0
	MsgSetEncodings             uint8 = 2
	MsgFramebufferUpdateRequest uint8 = 3
	MsgKeyEvent                 uint8 = 4
	MsgPointerEvent             uint8 = 5
	MsgClientCutText            uint8 = 6
)

// Server-to-client message types.
const (
	MsgFramebufferUpdate uint8 = 0
	MsgSetColourMapEntry uint8 = 1
	MsgBell              uint8 = 2
	MsgServerCutText     uint8 = 3
)

// Encoding types.
const (
	EncodingRaw     int32 = 0
	EncodingCopyRect int32 = 1
	EncodingRRE     int32 = 2
	EncodingZlib    int32 = 6
	EncodingTight   int32 = 7
	EncodingCursor  int32 = -239
	EncodingDesktopSize int32 = -223
)

// PixelFormat describes how a pixel is represented on the wire.
type PixelFormat struct {
	BitsPerPixel  uint8
	Depth         uint8
	BigEndian     uint8
	TrueColour    uint8
	RedMax        uint16
	GreenMax      uint16
	BlueMax       uint16
	RedShift      uint8
	GreenShift    uint8
	BlueShift     uint8
	_             [3]uint8 // padding
}

// DefaultPixelFormat returns a standard 32-bit BGRA pixel format.
func DefaultPixelFormat() PixelFormat {
	return PixelFormat{
		BitsPerPixel: 32,
		Depth:        24,
		BigEndian:    0,
		TrueColour:   1,
		RedMax:       255,
		GreenMax:     255,
		BlueMax:      255,
		RedShift:     16,
		GreenShift:   8,
		BlueShift:    0,
	}
}

// ServerInit is the server initialization message sent after security handshake.
type ServerInit struct {
	Width       uint16
	Height      uint16
	PixelFormat PixelFormat
	NameLength  uint32
	Name        string
}

// WriteServerInit writes a ServerInit message to the writer.
func WriteServerInit(w io.Writer, init *ServerInit) error {
	if err := binary.Write(w, binary.BigEndian, init.Width); err != nil {
		return fmt.Errorf("write width: %w", err)
	}
	if err := binary.Write(w, binary.BigEndian, init.Height); err != nil {
		return fmt.Errorf("write height: %w", err)
	}
	if err := binary.Write(w, binary.BigEndian, init.PixelFormat); err != nil {
		return fmt.Errorf("write pixel format: %w", err)
	}
	nameBytes := []byte(init.Name)
	if err := binary.Write(w, binary.BigEndian, uint32(len(nameBytes))); err != nil {
		return fmt.Errorf("write name length: %w", err)
	}
	if _, err := w.Write(nameBytes); err != nil {
		return fmt.Errorf("write name: %w", err)
	}
	return nil
}

// FramebufferUpdateRequest is sent by the client to request a framebuffer update.
type FramebufferUpdateRequest struct {
	Incremental uint8
	X           uint16
	Y           uint16
	Width       uint16
	Height      uint16
}

// ReadFramebufferUpdateRequest reads a FramebufferUpdateRequest from the reader.
// The message type byte must already be consumed.
func ReadFramebufferUpdateRequest(r io.Reader) (*FramebufferUpdateRequest, error) {
	req := &FramebufferUpdateRequest{}
	if err := binary.Read(r, binary.BigEndian, req); err != nil {
		return nil, fmt.Errorf("read framebuffer update request: %w", err)
	}
	return req, nil
}

// KeyEvent is sent by the client when a key is pressed or released.
type KeyEvent struct {
	DownFlag uint8
	_        [2]uint8 // padding
	Key      uint32
}

// ReadKeyEvent reads a KeyEvent from the reader. The message type byte must already be consumed.
func ReadKeyEvent(r io.Reader) (*KeyEvent, error) {
	evt := &KeyEvent{}
	if err := binary.Read(r, binary.BigEndian, evt); err != nil {
		return nil, fmt.Errorf("read key event: %w", err)
	}
	return evt, nil
}

// PointerEvent is sent by the client when the pointer moves or a button changes state.
type PointerEvent struct {
	ButtonMask uint8
	X          uint16
	Y          uint16
}

// ReadPointerEvent reads a PointerEvent from the reader. The message type byte must already be consumed.
func ReadPointerEvent(r io.Reader) (*PointerEvent, error) {
	evt := &PointerEvent{}
	if err := binary.Read(r, binary.BigEndian, evt); err != nil {
		return nil, fmt.Errorf("read pointer event: %w", err)
	}
	return evt, nil
}

// SetPixelFormatMsg represents the client's pixel format request.
type SetPixelFormatMsg struct {
	_           [3]uint8 // padding
	PixelFormat PixelFormat
}

// ReadSetPixelFormat reads a SetPixelFormat message. The message type byte must already be consumed.
func ReadSetPixelFormat(r io.Reader) (*SetPixelFormatMsg, error) {
	msg := &SetPixelFormatMsg{}
	if err := binary.Read(r, binary.BigEndian, msg); err != nil {
		return nil, fmt.Errorf("read set pixel format: %w", err)
	}
	return msg, nil
}

// ReadSetEncodings reads a SetEncodings message and returns the requested encodings.
// The message type byte must already be consumed.
func ReadSetEncodings(r io.Reader) ([]int32, error) {
	var padding uint8
	if err := binary.Read(r, binary.BigEndian, &padding); err != nil {
		return nil, fmt.Errorf("read padding: %w", err)
	}
	var count uint16
	if err := binary.Read(r, binary.BigEndian, &count); err != nil {
		return nil, fmt.Errorf("read encoding count: %w", err)
	}
	encodings := make([]int32, count)
	for i := range encodings {
		if err := binary.Read(r, binary.BigEndian, &encodings[i]); err != nil {
			return nil, fmt.Errorf("read encoding %d: %w", i, err)
		}
	}
	return encodings, nil
}

// ReadClientCutText reads client clipboard text. The message type byte must already be consumed.
func ReadClientCutText(r io.Reader) (string, error) {
	var padding [3]uint8
	if err := binary.Read(r, binary.BigEndian, &padding); err != nil {
		return "", fmt.Errorf("read padding: %w", err)
	}
	var length uint32
	if err := binary.Read(r, binary.BigEndian, &length); err != nil {
		return "", fmt.Errorf("read text length: %w", err)
	}
	if length > 10*1024*1024 { // 10MB limit
		return "", fmt.Errorf("cut text too large: %d bytes", length)
	}
	text := make([]byte, length)
	if _, err := io.ReadFull(r, text); err != nil {
		return "", fmt.Errorf("read text: %w", err)
	}
	return string(text), nil
}

// RectHeader is the header for a framebuffer update rectangle.
type RectHeader struct {
	X        uint16
	Y        uint16
	Width    uint16
	Height   uint16
	Encoding int32
}

// WriteFramebufferUpdate writes a framebuffer update with the given rectangles.
func WriteFramebufferUpdate(w io.Writer, rects []Rectangle) error {
	if err := binary.Write(w, binary.BigEndian, uint8(MsgFramebufferUpdate)); err != nil {
		return fmt.Errorf("write message type: %w", err)
	}
	// padding
	if err := binary.Write(w, binary.BigEndian, uint8(0)); err != nil {
		return fmt.Errorf("write padding: %w", err)
	}
	if err := binary.Write(w, binary.BigEndian, uint16(len(rects))); err != nil {
		return fmt.Errorf("write rect count: %w", err)
	}
	for i, rect := range rects {
		if err := rect.Write(w); err != nil {
			return fmt.Errorf("write rect %d: %w", i, err)
		}
	}
	return nil
}

// Rectangle represents a framebuffer update rectangle with header and encoded data.
type Rectangle struct {
	Header RectHeader
	Data   []byte
}

// Write writes the rectangle to the writer.
func (r *Rectangle) Write(w io.Writer) error {
	if err := binary.Write(w, binary.BigEndian, r.Header); err != nil {
		return fmt.Errorf("write rect header: %w", err)
	}
	if _, err := w.Write(r.Data); err != nil {
		return fmt.Errorf("write rect data: %w", err)
	}
	return nil
}

// CursorImage represents a cursor shape to be sent via the Cursor pseudo-encoding.
// Pixels are in the server's pixel format (BGRA by default), row-major order.
// Mask is a 1-bit-per-pixel bitmask, each row padded to a byte boundary (ceil(Width/8) bytes per row).
// A set bit means the corresponding pixel is opaque.
type CursorImage struct {
	Width, Height    uint16
	HotspotX, HotspotY uint16
	Pixels           []byte // width * height * bytesPerPixel
	Mask             []byte // ceil(width/8) * height bytes
}

// EncodeCursorRect creates a Rectangle for the Cursor pseudo-encoding.
// The hotspot is encoded in the RectHeader's X/Y fields per RFC 6143.
func EncodeCursorRect(cursor *CursorImage, pf PixelFormat, serverPf PixelFormat) Rectangle {
	w := int(cursor.Width)
	h := int(cursor.Height)
	pixels := ConvertPixels(pf, serverPf, cursor.Pixels, w, h)
	data := make([]byte, len(pixels)+len(cursor.Mask))
	copy(data, pixels)
	copy(data[len(pixels):], cursor.Mask)
	return Rectangle{
		Header: RectHeader{
			X:        cursor.HotspotX,
			Y:        cursor.HotspotY,
			Width:    cursor.Width,
			Height:   cursor.Height,
			Encoding: EncodingCursor,
		},
		Data: data,
	}
}

// EncodeDesktopSizeRect creates a Rectangle for the DesktopSize pseudo-encoding.
// Per RFC 6143, the rectangle carries no pixel data; width/height convey the new dimensions.
func EncodeDesktopSizeRect(width, height uint16) Rectangle {
	return Rectangle{
		Header: RectHeader{
			X:        0,
			Y:        0,
			Width:    width,
			Height:   height,
			Encoding: EncodingDesktopSize,
		},
		Data: nil,
	}
}

// WriteServerCutText sends clipboard text to the client.
func WriteServerCutText(w io.Writer, text string) error {
	if err := binary.Write(w, binary.BigEndian, uint8(MsgServerCutText)); err != nil {
		return err
	}
	if _, err := w.Write([]byte{0, 0, 0}); err != nil { // padding
		return err
	}
	textBytes := []byte(text)
	if err := binary.Write(w, binary.BigEndian, uint32(len(textBytes))); err != nil {
		return err
	}
	_, err := w.Write(textBytes)
	return err
}

// WriteBell sends a bell notification to the client.
func WriteBell(w io.Writer) error {
	return binary.Write(w, binary.BigEndian, uint8(MsgBell))
}
