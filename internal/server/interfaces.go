package server

import (
	"io"

	"github.com/khaiql/parley/internal/artifact"
	"github.com/khaiql/parley/internal/eventlog"
	"github.com/khaiql/parley/internal/model"
)

// Lifecycle is the interface for a Parley chat server.
// Implementations manage client connections and route messages.
type Lifecycle interface {
	Addr() string
	Port() int
	Serve()
	Close() error
}

type EventLog interface {
	Append(model.Event) (model.Event, error)
	ReadAll() ([]model.Event, error)
}

type ArtifactRepository interface {
	Stage(participant, name string, data []byte) (artifact.Metadata, error)
	StageReader(participant, name string, size int64, r io.Reader) (artifact.Metadata, error)
	Commit(participant string, ids []string) ([]artifact.Metadata, error)
	Open(id string) (io.ReadCloser, artifact.Metadata, error)
	CleanupStaged(participant string) error
	CleanupCommitted(ids []string) error
}

// Compile-time interface check.
var _ Lifecycle = (*Server)(nil)
var _ EventLog = (*eventlog.Log)(nil)
var _ ArtifactRepository = (*artifact.Store)(nil)
