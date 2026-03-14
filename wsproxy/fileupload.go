package wsproxy

import (
	"context"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"nhooyr.io/websocket"
)

const maxConcurrentUploads = 4

// handleUploadBegin processes an UploadBegin extension message (type 131).
func (p *Proxy) handleUploadBegin(ctx context.Context, data []byte) {
	// Minimum: type(1) + length(4) + uploadId(4) + fileSize(8) + dirLength(2) = 19 bytes
	if len(data) < 19 {
		log.Printf("session %s: upload begin too short", p.session.ID)
		return
	}

	uploadID := binary.BigEndian.Uint32(data[5:9])
	fileSize := binary.BigEndian.Uint64(data[9:17])
	dirLen := binary.BigEndian.Uint16(data[17:19])

	offset := 19
	if len(data) < offset+int(dirLen)+2 {
		p.sendUploadError(ctx, uploadID, 0, "message truncated")
		return
	}

	dir := string(data[offset : offset+int(dirLen)])
	offset += int(dirLen)

	nameLen := binary.BigEndian.Uint16(data[offset : offset+2])
	offset += 2

	if len(data) < offset+int(nameLen) {
		p.sendUploadError(ctx, uploadID, 0, "message truncated")
		return
	}
	fileName := string(data[offset : offset+int(nameLen)])

	// Validate concurrent upload limit
	if p.session.ActiveUploadCount() >= maxConcurrentUploads {
		p.sendUploadError(ctx, uploadID, 0, "too many concurrent uploads")
		return
	}

	// Validate file size
	if int64(fileSize) > p.server.config.MaxUploadSize {
		p.sendUploadError(ctx, uploadID, 0, fmt.Sprintf("file too large: %d bytes (max %d)", fileSize, p.server.config.MaxUploadSize))
		return
	}

	// Resolve upload directory
	if dir == "" {
		dir = p.server.config.DefaultUploadDir
	}

	// Validate directory
	if !p.server.IsUploadDirAllowed(dir) {
		p.sendUploadError(ctx, uploadID, 0, "directory not allowed")
		return
	}

	// Sanitize filename
	sanitized, err := SanitizeFilename(fileName)
	if err != nil {
		p.sendUploadError(ctx, uploadID, 0, fmt.Sprintf("invalid filename: %v", err))
		return
	}

	// Validate no path traversal in filename
	if strings.Contains(sanitized, "..") {
		p.sendUploadError(ctx, uploadID, 0, "invalid filename")
		return
	}

	// Create directory if needed
	if err := os.MkdirAll(dir, 0755); err != nil {
		p.sendUploadError(ctx, uploadID, 0, fmt.Sprintf("cannot create directory: %v", err))
		return
	}

	// Get unique file path
	filePath := UniqueFilePath(dir, sanitized)

	// Create file
	f, err := os.Create(filePath)
	if err != nil {
		p.sendUploadError(ctx, uploadID, 0, fmt.Sprintf("cannot create file: %v", err))
		return
	}

	upload := &uploadState{
		ID:       uploadID,
		FileName: filepath.Base(filePath),
		FilePath: filePath,
		FileSize: fileSize,
		File:     f,
		Closer:   f,
	}
	p.session.AddUpload(upload)

	log.Printf("session %s: upload %d started: %s (%d bytes)", p.session.ID, uploadID, filePath, fileSize)

	// Send success status
	p.sendUploadStatus(ctx, uploadID, 0, 0, fmt.Sprintf("upload started: %s", upload.FileName))
}

// handleUploadChunk processes an UploadChunk extension message (type 132).
func (p *Proxy) handleUploadChunk(ctx context.Context, data []byte) {
	// Minimum: type(1) + length(4) + uploadId(4) + offset(8) = 17 bytes
	if len(data) < 17 {
		log.Printf("session %s: upload chunk too short", p.session.ID)
		return
	}

	uploadID := binary.BigEndian.Uint32(data[5:9])
	fileOffset := binary.BigEndian.Uint64(data[9:17])
	chunkData := data[17:]

	upload := p.session.GetUpload(uploadID)
	if upload == nil {
		p.sendUploadError(ctx, uploadID, 0, "unknown upload ID")
		return
	}

	// Validate chunk doesn't exceed file size
	if fileOffset+uint64(len(chunkData)) > upload.FileSize {
		p.sendUploadError(ctx, uploadID, upload.BytesWritten, "chunk exceeds declared file size")
		return
	}

	// Validate running total doesn't exceed max upload size
	newTotal := upload.BytesWritten + uint64(len(chunkData))
	if int64(newTotal) > p.server.config.MaxUploadSize {
		p.sendUploadError(ctx, uploadID, upload.BytesWritten, "upload exceeds maximum size")
		return
	}

	// Write chunk at offset
	if _, err := upload.File.WriteAt(chunkData, int64(fileOffset)); err != nil {
		p.sendUploadError(ctx, uploadID, upload.BytesWritten, fmt.Sprintf("write error: %v", err))
		return
	}

	upload.BytesWritten = newTotal

	// Send progress status
	p.sendUploadStatus(ctx, uploadID, 0, upload.BytesWritten, "")
}

// handleUploadEnd processes an UploadEnd extension message (type 133).
func (p *Proxy) handleUploadEnd(ctx context.Context, data []byte) {
	// type(1) + length(4) + uploadId(4) + checksum(4) = 13 bytes
	if len(data) < 13 {
		log.Printf("session %s: upload end too short", p.session.ID)
		return
	}

	uploadID := binary.BigEndian.Uint32(data[5:9])
	expectedCRC := binary.BigEndian.Uint32(data[9:13])

	upload := p.session.RemoveUpload(uploadID)
	if upload == nil {
		p.sendUploadError(ctx, uploadID, 0, "unknown upload ID")
		return
	}
	defer upload.Closer.Close()

	// Verify CRC-32
	f, err := os.Open(upload.FilePath)
	if err != nil {
		p.sendUploadError(ctx, uploadID, upload.BytesWritten, fmt.Sprintf("cannot open file for verification: %v", err))
		return
	}
	defer f.Close()

	hash := crc32.NewIEEE()
	if _, err := io.Copy(hash, f); err != nil {
		p.sendUploadError(ctx, uploadID, upload.BytesWritten, fmt.Sprintf("checksum computation error: %v", err))
		return
	}

	actualCRC := hash.Sum32()
	if actualCRC != expectedCRC {
		// Delete the file on checksum mismatch
		os.Remove(upload.FilePath)
		p.sendUploadError(ctx, uploadID, upload.BytesWritten, fmt.Sprintf("checksum mismatch: expected %08x, got %08x", expectedCRC, actualCRC))
		return
	}

	log.Printf("session %s: upload %d completed: %s", p.session.ID, uploadID, upload.FilePath)
	p.sendUploadStatus(ctx, uploadID, 0, upload.BytesWritten, "upload complete")
}

// handleUploadCancel processes an UploadCancel extension message (type 135).
func (p *Proxy) handleUploadCancel(ctx context.Context, data []byte) {
	// type(1) + length(4) + uploadId(4) = 9 bytes
	if len(data) < 9 {
		log.Printf("session %s: upload cancel too short", p.session.ID)
		return
	}

	uploadID := binary.BigEndian.Uint32(data[5:9])

	upload := p.session.RemoveUpload(uploadID)
	if upload == nil {
		return
	}

	upload.Closer.Close()
	os.Remove(upload.FilePath)

	log.Printf("session %s: upload %d cancelled", p.session.ID, uploadID)
	p.sendUploadError(ctx, uploadID, upload.BytesWritten, "cancelled")
}

// sendUploadStatus sends an UploadStatus extension message (type 134) to the browser.
func (p *Proxy) sendUploadStatus(ctx context.Context, uploadID uint32, status uint8, bytesWritten uint64, message string) {
	msgBytes := []byte(message)
	// payload: uploadId(4) + status(1) + bytesWritten(8) + messageLength(2) + message(n)
	payloadLen := 4 + 1 + 8 + 2 + len(msgBytes)

	buf := make([]byte, 5+payloadLen)
	buf[0] = ExtUploadStatus
	binary.BigEndian.PutUint32(buf[1:5], uint32(payloadLen))
	binary.BigEndian.PutUint32(buf[5:9], uploadID)
	buf[9] = status
	binary.BigEndian.PutUint64(buf[10:18], bytesWritten)
	binary.BigEndian.PutUint16(buf[18:20], uint16(len(msgBytes)))
	copy(buf[20:], msgBytes)

	if err := p.session.WSConn.Write(ctx, websocket.MessageBinary, buf); err != nil {
		log.Printf("session %s: failed to send upload status: %v", p.session.ID, err)
	}
}

// sendUploadError is a convenience wrapper for sendUploadStatus with status=1.
func (p *Proxy) sendUploadError(ctx context.Context, uploadID uint32, bytesWritten uint64, message string) {
	p.sendUploadStatus(ctx, uploadID, 1, bytesWritten, message)
}
