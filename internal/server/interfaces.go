package server

// Server is the interface for a Parley chat server.
// Implementations manage client connections and route messages.
type Server interface {
	Addr() string
	Port() int
	Serve()
	Close() error
}

// Compile-time interface check.
var _ Server = (*TCPServer)(nil)
