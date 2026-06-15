package eventlog

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/khaiql/parley/internal/model"
)

type Log struct {
	path string
	mu   sync.Mutex
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
	if err := os.MkdirAll(filepath.Dir(l.path), 0o700); err != nil {
		return model.Event{}, err
	}
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return model.Event{}, err
	}
	defer f.Close()
	data, err := json.Marshal(ev)
	if err != nil {
		return model.Event{}, err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		return model.Event{}, err
	}
	_ = os.Chmod(l.path, 0o600)
	return ev, nil
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

	var events []model.Event
	sc := bufio.NewScanner(f)
	line := 0
	for sc.Scan() {
		line++
		var ev model.Event
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", l.path, line, err)
		}
		events = append(events, ev)
	}
	return events, sc.Err()
}
