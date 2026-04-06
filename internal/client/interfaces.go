package client

import "github.com/khaiql/parley/internal/protocol"

// Client is the interface for connecting to a Parley server.
type Client interface {
	Incoming() <-chan *protocol.RawMessage
	Join(params protocol.JoinParams) error
	Send(content protocol.Content, mentions []string) error
	SendStatus(name, status string) error
	Close() error
}

// Compile-time interface check.
var _ Client = (*TCPClient)(nil)
