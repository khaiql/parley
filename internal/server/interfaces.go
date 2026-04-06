package server

// Server is the interface for a Parley chat server.
// Implementations manage client connections and route messages through a Room.
type Server interface {
	Addr() string
	Port() int
	Room() *Room
	Serve()
	Close() error
}

// Compile-time interface check.
var _ Server = (*TcpServer)(nil)
