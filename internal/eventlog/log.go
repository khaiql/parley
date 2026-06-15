package eventlog

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/khaiql/parley/internal/model"
)

type Log struct {
	path string

	// mu serializes operations within this process; it is not an inter-process file lock.
	mu sync.Mutex
}

func New(path string) *Log {
	return &Log{path: path}
}

func (l *Log) Append(ev model.Event) (model.Event, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	events, err := l.readAllLocked()
	if err != nil {
		return model.Event{}, err
	}
	var last int64
	if len(events) > 0 {
		last = events[len(events)-1].Seq
	}
	ev.Seq = last + 1
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now().UTC()
	}
	if err := l.writeEventLocked(ev); err != nil {
		return model.Event{}, err
	}
	return ev, nil
}

func (l *Log) AppendAssigned(ev model.Event) error {
	if ev.Seq <= 0 {
		return fmt.Errorf("event seq must be positive")
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	return l.writeEventLocked(ev)
}

func (l *Log) writeEventLocked(ev model.Event) (err error) {
	if err := os.MkdirAll(filepath.Dir(l.path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := f.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("%s: close: %w", l.path, closeErr)
		}
	}()
	if err := os.Chmod(l.path, 0o600); err != nil {
		return fmt.Errorf("%s: chmod: %w", l.path, err)
	}
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}

func (l *Log) ReadAll() ([]model.Event, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.readAllLocked()
}

func (l *Log) AfterSeq(seq int64, limit int) ([]model.Event, error) {
	all, err := l.ReadAll()
	if err != nil {
		return nil, err
	}
	out := make([]model.Event, 0)
	for _, ev := range all {
		if ev.Seq > seq {
			out = append(out, ev)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (l *Log) readAllLocked() ([]model.Event, error) {
	f, err := os.Open(l.path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	r := bufio.NewReader(f)
	line := 0
	var events []model.Event
	for {
		data, err := r.ReadBytes('\n')
		if len(data) > 0 {
			line++
			var ev model.Event
			if err := json.Unmarshal(data, &ev); err != nil {
				return nil, fmt.Errorf("%s:%d: %w", l.path, line, err)
			}
			events = append(events, ev)
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			errLine := line
			if len(data) == 0 {
				errLine++
			}
			return nil, fmt.Errorf("%s:%d: %w", l.path, errLine, err)
		}
	}
	return events, nil
}
