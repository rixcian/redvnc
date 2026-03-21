// Package wsproxy implements a WebSocket-to-TCP proxy that bridges browser clients to VNC servers.
package wsproxy

import (
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"nhooyr.io/websocket"
)

// Session represents a single client-proxy-VNC session.
type Session struct {
	ID            string
	ClientAddr    string
	Target        string
	WSConn        *websocket.Conn
	TCPConn       net.Conn
	CreatedAt     time.Time
	LastActivity  time.Time
	Password      string

	// Bandwidth tracking (bytes transferred, updated atomically)
	BytesToClient   atomic.Int64
	BytesFromClient atomic.Int64

	// Upload state
	mu      sync.Mutex
	uploads map[uint32]*uploadState
}

// uploadState tracks an in-progress file upload.
type uploadState struct {
	ID           uint32
	FileName     string
	FilePath     string
	FileSize     uint64
	BytesWritten uint64
	File         interface{ WriteAt([]byte, int64) (int, error) }
	Closer       interface{ Close() error }
}

// NewSession creates a new session with a generated UUID.
func NewSession(clientAddr, target, password string, wsConn *websocket.Conn) *Session {
	now := time.Now()
	return &Session{
		ID:           uuid.New().String(),
		ClientAddr:   clientAddr,
		Target:       target,
		WSConn:       wsConn,
		CreatedAt:    now,
		LastActivity: now,
		Password:     password,
		uploads:      make(map[uint32]*uploadState),
	}
}

// TouchActivity updates the last activity timestamp.
func (s *Session) TouchActivity() {
	s.mu.Lock()
	s.LastActivity = time.Now()
	s.mu.Unlock()
}

// AddUpload registers an upload in the session.
func (s *Session) AddUpload(u *uploadState) {
	s.mu.Lock()
	s.uploads[u.ID] = u
	s.mu.Unlock()
}

// GetUpload returns the upload state for the given ID.
func (s *Session) GetUpload(id uint32) *uploadState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.uploads[id]
}

// RemoveUpload removes and returns the upload state for the given ID.
func (s *Session) RemoveUpload(id uint32) *uploadState {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.uploads[id]
	delete(s.uploads, id)
	return u
}

// ActiveUploadCount returns the number of active uploads.
func (s *Session) ActiveUploadCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.uploads)
}

// CleanupUploads closes all active upload files.
func (s *Session) CleanupUploads() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.uploads {
		if u.Closer != nil {
			u.Closer.Close()
		}
	}
	s.uploads = make(map[uint32]*uploadState)
}

// ConnectionManager tracks all active sessions and enforces connection limits.
type ConnectionManager struct {
	mu                sync.Mutex
	sessions          map[string]*Session
	targetCounts      map[string]int
	maxConnections    int
	maxPerTarget      int
}

// NewConnectionManager creates a new connection manager.
func NewConnectionManager(maxConnections, maxPerTarget int) *ConnectionManager {
	if maxConnections <= 0 {
		maxConnections = 100
	}
	if maxPerTarget <= 0 {
		maxPerTarget = 10
	}
	return &ConnectionManager{
		sessions:       make(map[string]*Session),
		targetCounts:   make(map[string]int),
		maxConnections: maxConnections,
		maxPerTarget:   maxPerTarget,
	}
}

// TryAdd attempts to add a session. Returns false if limits are exceeded.
func (cm *ConnectionManager) TryAdd(s *Session) bool {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if len(cm.sessions) >= cm.maxConnections {
		return false
	}
	if cm.targetCounts[s.Target] >= cm.maxPerTarget {
		return false
	}

	cm.sessions[s.ID] = s
	cm.targetCounts[s.Target]++
	return true
}

// Remove removes a session from tracking.
func (cm *ConnectionManager) Remove(s *Session) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if _, ok := cm.sessions[s.ID]; !ok {
		return
	}
	delete(cm.sessions, s.ID)
	cm.targetCounts[s.Target]--
	if cm.targetCounts[s.Target] <= 0 {
		delete(cm.targetCounts, s.Target)
	}
}

// All returns a snapshot of all active sessions.
func (cm *ConnectionManager) All() []*Session {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	result := make([]*Session, 0, len(cm.sessions))
	for _, s := range cm.sessions {
		result = append(result, s)
	}
	return result
}

// Count returns the total number of active sessions.
func (cm *ConnectionManager) Count() int {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	return len(cm.sessions)
}
