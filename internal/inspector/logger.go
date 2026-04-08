package inspector

import (
	"sync"
	"time"
)

const (
	maxEntries = 50
	maxBodyLog = 1 << 20 // 1 MB
)

// TrafficLog represents a captured HTTP request/response pair.
type TrafficLog struct {
	ID             int               `json:"id"`
	Method         string            `json:"method"`
	Path           string            `json:"path"`
	ReqHeaders     map[string]string `json:"req_headers"`
	ReqBody        string            `json:"req_body"`
	RespStatus     int               `json:"resp_status"`
	RespHeaders    map[string]string `json:"resp_headers"`
	RespBody       string            `json:"resp_body"`
	RespBodyBinary bool              `json:"resp_body_binary,omitempty"`
	Duration       time.Duration     `json:"duration"`
	Timestamp      time.Time         `json:"timestamp"`
}

// Logger is a thread-safe ring buffer of TrafficLog entries.
type Logger struct {
	mu      sync.RWMutex
	entries []TrafficLog
	nextID  int
}

// NewLogger creates an empty Logger.
func NewLogger() *Logger {
	return &Logger{}
}

// Add appends a TrafficLog entry and returns its assigned ID.
// The buffer is capped at maxEntries; oldest entries are evicted first.
func (l *Logger) Add(entry TrafficLog) int {
	l.mu.Lock()
	defer l.mu.Unlock()

	entry.ID = l.nextID
	l.nextID++
	l.entries = append(l.entries, entry)
	if len(l.entries) > maxEntries {
		l.entries = l.entries[len(l.entries)-maxEntries:]
	}
	return entry.ID
}

// Entries returns a copy of all stored log entries.
func (l *Logger) Entries() []TrafficLog {
	l.mu.RLock()
	defer l.mu.RUnlock()

	out := make([]TrafficLog, len(l.entries))
	copy(out, l.entries)
	return out
}

// TruncateBody caps a body at maxBodyLog bytes for logging purposes.
func TruncateBody(body []byte) string {
	if len(body) > maxBodyLog {
		return string(body[:maxBodyLog])
	}
	return string(body)
}
