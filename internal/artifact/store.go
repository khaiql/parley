package artifact

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

type Store struct {
	root string

	mu        sync.Mutex
	staged    map[string]stagedArtifact
	committed map[string]committedArtifact
}

type stagedArtifact struct {
	participant string
	meta        Metadata
	path        string
}

type committedArtifact struct {
	meta Metadata
	path string
}

func NewStore(roomDir string) *Store {
	return &Store{
		root:      filepath.Join(roomDir, "artifacts"),
		staged:    make(map[string]stagedArtifact),
		committed: make(map[string]committedArtifact),
	}
}

func (s *Store) Stage(participant, name string, data []byte) (Metadata, error) {
	return s.StageReader(participant, name, int64(len(data)), bytes.NewReader(data))
}

func (s *Store) StageReader(participant, name string, size int64, r io.Reader) (Metadata, error) {
	if participant == "" {
		return Metadata{}, Error{Code: "bad_request", Message: "participant is required"}
	}
	if size > MaxFileBytes {
		return Metadata{}, Error{Code: "artifact_too_large", Message: fmt.Sprintf("artifact exceeds %d bytes", MaxFileBytes)}
	}
	id, err := newID()
	if err != nil {
		return Metadata{}, err
	}
	dir := filepath.Join(s.root, "staged", participant)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Metadata{}, err
	}
	path := filepath.Join(dir, id)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return Metadata{}, err
	}

	hash := sha256.New()
	written, copyErr := io.CopyN(io.MultiWriter(file, hash), r, MaxFileBytes)
	if errors.Is(copyErr, io.EOF) {
		copyErr = nil
	} else if copyErr == nil {
		var extra [1]byte
		n, err := r.Read(extra[:])
		if err != nil && !errors.Is(err, io.EOF) {
			copyErr = err
		} else if n > 0 {
			copyErr = Error{Code: "artifact_too_large", Message: fmt.Sprintf("artifact exceeds %d bytes", MaxFileBytes)}
		}
	}
	closeErr := file.Close()
	if copyErr != nil {
		_ = os.Remove(path)
		return Metadata{}, copyErr
	}
	if closeErr != nil {
		_ = os.Remove(path)
		return Metadata{}, closeErr
	}
	meta := Metadata{
		ID:     id,
		Name:   SanitizeName(name),
		Size:   written,
		SHA256: hex.EncodeToString(hash.Sum(nil)),
	}

	s.mu.Lock()
	s.staged[id] = stagedArtifact{participant: participant, meta: meta, path: path}
	s.mu.Unlock()
	return meta, nil
}

func (s *Store) Commit(participant string, ids []string) ([]Metadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(ids) > MaxFilesPerMessage {
		return nil, Error{Code: "too_many_artifacts", Message: fmt.Sprintf("at most %d artifacts are allowed", MaxFilesPerMessage)}
	}
	var total int64
	metas := make([]Metadata, 0, len(ids))
	staged := make([]stagedArtifact, 0, len(ids))
	for _, id := range ids {
		art, ok := s.staged[id]
		if !ok || art.participant != participant {
			return nil, Error{Code: "artifact_unavailable", Message: fmt.Sprintf("artifact is not staged for participant: %s", id)}
		}
		total += art.meta.Size
		if total > MaxTotalBytesPerMessage {
			return nil, Error{Code: "artifact_batch_too_large", Message: fmt.Sprintf("artifact batch exceeds %d bytes", MaxTotalBytesPerMessage)}
		}
		staged = append(staged, art)
		metas = append(metas, art.meta)
	}

	committedDir := filepath.Join(s.root, "committed")
	if err := os.MkdirAll(committedDir, 0o700); err != nil {
		return nil, err
	}
	type promotedArtifact struct {
		staged stagedArtifact
		dst    string
	}
	promoted := make([]promotedArtifact, 0, len(staged))
	for _, art := range staged {
		dst := filepath.Join(committedDir, art.meta.ID)
		if err := os.Rename(art.path, dst); err != nil {
			for i := len(promoted) - 1; i >= 0; i-- {
				_ = os.Rename(promoted[i].dst, promoted[i].staged.path)
			}
			return nil, err
		}
		promoted = append(promoted, promotedArtifact{staged: art, dst: dst})
	}
	for _, promotedArt := range promoted {
		art := promotedArt.staged
		delete(s.staged, art.meta.ID)
		s.committed[art.meta.ID] = committedArtifact{meta: art.meta, path: promotedArt.dst}
	}
	return metas, nil
}

func (s *Store) Open(id string) (io.ReadCloser, Metadata, error) {
	s.mu.Lock()
	art, ok := s.committed[id]
	s.mu.Unlock()
	if !ok {
		return nil, Metadata{}, Error{Code: "artifact_unavailable", Message: fmt.Sprintf("artifact is not available: %s", id)}
	}
	file, err := os.Open(art.path)
	if err != nil {
		return nil, Metadata{}, err
	}
	return file, art.meta, nil
}

func (s *Store) CleanupStaged(participant string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var firstErr error
	for id, art := range s.staged {
		if art.participant != participant {
			continue
		}
		if err := os.Remove(art.path); err != nil && !os.IsNotExist(err) && firstErr == nil {
			firstErr = err
		}
		delete(s.staged, id)
	}
	return firstErr
}

func (s *Store) CleanupCommitted(ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var firstErr error
	for _, id := range ids {
		art, ok := s.committed[id]
		if !ok {
			continue
		}
		if err := os.Remove(art.path); err != nil && !os.IsNotExist(err) && firstErr == nil {
			firstErr = err
		}
		delete(s.committed, id)
	}
	return firstErr
}

func (s *Store) CleanupAll() error {
	s.mu.Lock()
	s.staged = make(map[string]stagedArtifact)
	s.committed = make(map[string]committedArtifact)
	s.mu.Unlock()
	return os.RemoveAll(s.root)
}

func newID() (string, error) {
	var b [10]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "art_" + hex.EncodeToString(b[:]), nil
}
