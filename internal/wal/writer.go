package wal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Entry is a single WAL log line.
type Entry struct {
	Timestamp string          `json:"ts"`
	Agent     string          `json:"agent"`
	Direction string          `json:"dir"`
	Raw       json.RawMessage `json:"raw"`
}

// Writer is a thread-safe append-only NDJSON log writer.
type Writer struct {
	agent string
	f     *os.File
	enc   *json.Encoder
	mu    sync.Mutex
}

// New creates a WAL writer at the given path. Parent directories are created
// if they don't exist. The file is opened in append mode.
func New(path string, agent string) (*Writer, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	return &Writer{
		agent: agent,
		f:     f,
		enc:   json.NewEncoder(f),
	}, nil
}

// Log appends one NDJSON entry. Direction should be "in" or "out".
func (w *Writer) Log(direction string, raw []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.enc.Encode(Entry{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Agent:     w.agent,
		Direction: direction,
		Raw:       json.RawMessage(raw),
	})
}

// Close flushes and closes the underlying file.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.f.Close()
}
