package server

// Lifecycle is the interface for a Parley chat server.
// Implementations manage client connections and route messages.
type Lifecycle interface {
	Addr() string
	Port() int
	Serve()
	Close() error
}

// Compile-time interface check.
var _ Lifecycle = (*Server)(nil)
