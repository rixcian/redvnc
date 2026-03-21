package wsproxy

import (
	"context"
	"encoding/binary"

	"nhooyr.io/websocket"
)

// handleClipboardSet processes a ClipboardSet extension message (type 129)
// from the browser and forwards it as a standard RFB ClientCutText to the VNC server.
func (p *Proxy) handleClipboardSet(ctx context.Context, data []byte) {
	// Minimum: type(1) + length(4) + textLength(4) = 9 bytes
	if len(data) < 9 {
		p.server.logger.Warn("clipboard set message too short", "session_id", p.session.ID)
		return
	}

	payloadLen := binary.BigEndian.Uint32(data[1:5])
	if len(data) < int(5+payloadLen) {
		p.server.logger.Warn("clipboard set message truncated", "session_id", p.session.ID)
		return
	}

	textLen := binary.BigEndian.Uint32(data[5:9])
	if len(data) < int(9+textLen) {
		p.server.logger.Warn("clipboard set text truncated", "session_id", p.session.ID)
		return
	}

	text := data[9 : 9+textLen]

	// Build standard RFB ClientCutText message (type 6):
	// type(1) + padding(3) + textLength(4) + text(n)
	rfbMsg := make([]byte, 8+len(text))
	rfbMsg[0] = 6 // MsgClientCutText
	// padding bytes 1-3 are zero
	binary.BigEndian.PutUint32(rfbMsg[4:8], uint32(len(text)))
	copy(rfbMsg[8:], text)

	if _, err := p.session.TCPConn.Write(rfbMsg); err != nil {
		p.server.logger.Warn("failed to send clipboard to VNC", "session_id", p.session.ID, "error", err)
	}
}

// sendClipboardUpdate sends a ClipboardUpdate extension message (type 130)
// to the browser. Called when the proxy intercepts a ServerCutText from the VNC server.
func (p *Proxy) sendClipboardUpdate(ctx context.Context, text []byte) error {
	// Build extension message: type(1) + length(4) + textLength(4) + text(n)
	payloadLen := 4 + len(text)
	buf := make([]byte, 5+payloadLen)
	buf[0] = ExtClipboardUpdate
	binary.BigEndian.PutUint32(buf[1:5], uint32(payloadLen))
	binary.BigEndian.PutUint32(buf[5:9], uint32(len(text)))
	copy(buf[9:], text)

	return p.session.WSConn.Write(ctx, websocket.MessageBinary, buf)
}
