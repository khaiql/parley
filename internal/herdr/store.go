package herdr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

type Store struct{ dir string }

func NewStore(dir string) *Store { return &Store{dir: dir} }
func privateDir(dir string) error {
	info, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		if err = os.MkdirAll(dir, 0700); err != nil {
			return err
		}
		info, err = os.Lstat(dir)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("private directory is not a directory: %s", dir)
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); !ok || stat.Uid != uint32(os.Getuid()) {
		return fmt.Errorf("private directory is not owned by current user: %s", dir)
	}
	return os.Chmod(dir, 0700)
}
func privateFile(path string, flags int) (*os.File, error) {
	fd, err := syscall.Open(path, flags|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || !ok || stat.Uid != uint32(os.Getuid()) || info.Mode().Perm()&0077 != 0 {
		f.Close()
		return nil, fmt.Errorf("record must be private, regular, and owned by current user: %s", path)
	}
	return f, nil
}
func lockFile(ctx context.Context, path string, fn func() error) error {
	f, err := privateFile(path, syscall.O_CREAT|syscall.O_RDWR)
	if err != nil {
		return err
	}
	defer f.Close()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return err
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}
func (s *Store) locked(fn func() error) error {
	if err := privateDir(s.dir); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return lockFile(ctx, filepath.Join(s.dir, "state.lock"), fn)
}

// WithBinding holds a host transaction lock across processes. Callers acquire route:
// before join: and never hold the short data lock across external operations.
func (s *Store) WithBinding(ctx context.Context, key string, fn func() error) error {
	if (!strings.HasPrefix(key, "route:") && !strings.HasPrefix(key, "join:")) || strings.HasSuffix(key, ":") {
		return fmt.Errorf("binding lock requires route: or join: key")
	}
	if err := privateDir(s.dir); err != nil {
		return err
	}
	dir := filepath.Join(s.dir, "locks")
	if err := privateDir(dir); err != nil {
		return err
	}
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return lockFile(bounded, filepath.Join(dir, hashBytes([]byte(key))+".lock"), fn)
}
func readJSON(path string, v any) error {
	f, err := privateFile(path, syscall.O_RDONLY)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	if !utf8.Valid(b) {
		return fmt.Errorf("corrupt record %s: invalid UTF-8", path)
	}
	if len(bytes.TrimSpace(b)) == 0 || bytes.TrimSpace(b)[0] != '{' {
		return fmt.Errorf("invalid record: %s", path)
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return fmt.Errorf("corrupt record %s: %w", path, err)
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("record has trailing data: %s", path)
	}
	return nil
}
func writeJSON(path string, v any) error {
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return fmt.Errorf("unsafe record: %s", path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".record-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err := f.Chmod(0600); err != nil {
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
func emptyState() *State {
	return &State{Peers: map[string]Peer{}, Rooms: map[string]Room{}, Bindings: map[string]Binding{}, Requests: map[string]Outcome{}}
}
func (s *Store) read() (State, error) {
	v := emptyState()
	err := readJSON(filepath.Join(s.dir, "state.json"), v)
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	if v.Peers == nil {
		v.Peers = map[string]Peer{}
	}
	if v.Rooms == nil {
		v.Rooms = map[string]Room{}
	}
	if v.Bindings == nil {
		v.Bindings = map[string]Binding{}
	}
	if err != nil {
		return *v, err
	}
	v.Requests = map[string]Outcome{}
	dir, err := s.recordDir("outcomes")
	if err != nil {
		return *v, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return *v, err
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		if err := safeID(id); err != nil {
			return *v, err
		}
		outcome, err := s.readOutcome(id)
		if err != nil {
			return *v, err
		}
		v.Requests[id] = outcome
	}
	return *v, nil
}

// Read returns Requests as a derived view of authoritative outcome records.
func (s *Store) Read() (State, error) {
	var v State
	err := s.locked(func() error { var err error; v, err = s.read(); return err })
	return v, err
}

// Update changes session state; Requests is read-only. Use Claim/Complete for outcomes.
func (s *Store) Update(fn func(*State) error) error {
	return s.locked(func() error {
		v, err := s.read()
		if err != nil {
			return err
		}
		before, err := json.Marshal(v.Requests)
		if err != nil {
			return err
		}
		if err := fn(&v); err != nil {
			return err
		}
		after, err := json.Marshal(v.Requests)
		if err != nil {
			return err
		}
		if !bytes.Equal(before, after) {
			return fmt.Errorf("Requests is read-only; use Claim or Complete")
		}
		v.Requests = nil
		return writeJSON(filepath.Join(s.dir, "state.json"), v)
	})
}
func (s *Store) recordDir(name string) (string, error) {
	dir := filepath.Join(s.dir, name)
	return dir, privateDir(dir)
}
func (s *Store) Enqueue(req Request) error {
	if err := validateRequest(req); err != nil {
		return err
	}
	return s.locked(func() error {
		dir, err := s.recordDir("requests")
		if err != nil {
			return err
		}
		path := filepath.Join(dir, req.ID+".json")
		var prior Request
		err = readJSON(path, &prior)
		if err == nil {
			if prior != req {
				return fmt.Errorf("request_conflict")
			}
			return nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return writeJSON(path, req)
	})
}
func (s *Store) readOutcome(id string) (Outcome, error) {
	dir, err := s.recordDir("outcomes")
	if err != nil {
		return Outcome{}, err
	}
	var o Outcome
	err = readJSON(filepath.Join(dir, id+".json"), &o)
	if err == nil {
		err = validateOutcome(o)
	}
	return o, err
}
func validateOutcome(o Outcome) error {
	if !validDigest(o.Digest) || safeID(o.ControllerID) != nil {
		return fmt.Errorf("invalid outcome identity")
	}
	switch o.Phase {
	case "claimed", "intent", "complete", "uncertain":
		return nil
	default:
		return fmt.Errorf("invalid outcome phase")
	}
}
func (s *Store) Pending() ([]Request, error) {
	out := []Request{}
	err := s.locked(func() error {
		dir, err := s.recordDir("requests")
		if err != nil {
			return err
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			id := strings.TrimSuffix(e.Name(), ".json")
			if err := safeID(id); err != nil {
				return err
			}
			var req Request
			if err := readJSON(filepath.Join(dir, e.Name()), &req); err != nil {
				return err
			}
			if req.ID != id {
				return fmt.Errorf("request id mismatch")
			}
			if err := validateRequest(req); err != nil {
				return err
			}
			o, err := s.readOutcome(id)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}

			if o.Phase != "complete" {
				out = append(out, req)
			}
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, err
}

// Claim records a digest computed by the trusted caller after origin normalization;
// queued host-local records may still contain self, so their raw digest differs.
func (s *Store) Claim(requestID, digest, controllerID string) (Outcome, error) {
	var out Outcome
	if err := safeID(requestID); err != nil {
		return out, err
	}
	if err := safeID(controllerID); err != nil {
		return out, err
	}
	if !validDigest(digest) {
		return out, fmt.Errorf("invalid request digest")
	}
	err := s.locked(func() error {
		dir, err := s.recordDir("requests")
		if err != nil {
			return err
		}
		var req Request
		if err := readJSON(filepath.Join(dir, requestID+".json"), &req); err != nil {
			return err
		}
		if req.ID != requestID {
			return fmt.Errorf("request_conflict")
		}
		if err := validateRequest(req); err != nil {
			return err
		}
		prior, err := s.readOutcome(requestID)
		if err == nil {
			if prior.Digest != digest || prior.ControllerID != controllerID {
				return fmt.Errorf("request_conflict")
			}
			out = prior
			return nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		out = Outcome{Digest: digest, ControllerID: controllerID, Phase: "claimed"}
		return writeJSON(filepath.Join(s.dir, "outcomes", requestID+".json"), out)
	})
	return out, err
}
func (s *Store) Complete(requestID string, out Outcome) error {
	if err := safeID(requestID); err != nil {
		return err
	}
	if err := validateOutcome(out); err != nil {
		return err
	}
	return s.locked(func() error {
		prior, err := s.readOutcome(requestID)
		if err != nil {
			return err
		}
		if prior.Digest != out.Digest || prior.ControllerID != out.ControllerID {
			return fmt.Errorf("request_conflict")
		}
		if prior.Phase == "complete" {
			if !reflect.DeepEqual(prior, out) {
				return fmt.Errorf("request_conflict")
			}
			return nil
		}
		if out.Phase == "claimed" || prior.Phase == "uncertain" && out.Phase == "intent" {
			return fmt.Errorf("invalid outcome phase transition: %s to %s", prior.Phase, out.Phase)
		}
		return writeJSON(filepath.Join(s.dir, "outcomes", requestID+".json"), out)
	})
}

func validDigest(digest string) bool {
	if len(digest) != 64 {
		return false
	}
	for _, c := range digest {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
