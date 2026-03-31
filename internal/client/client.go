// Package client provides a TCP client for the Parley protocol.
package client

import (
	"bufio"
	"net"
	"sync"

	"github.com/khaiql/parley/internal/protocol"
)

// Client manages a single TCP connection to a Parley server.
type Client struct {
	conn      net.Conn
	incoming  chan *protocol.RawMessage
	done      chan struct{}
	closeOnce sync.Once
}

// New dials the server at addr, starts the read loop goroutine, and returns
// the Client. The caller must call Close when finished.
func New(addr string) (*Client, error) {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return nil, err
	}
	c := &Client{
		conn:     conn,
		incoming: make(chan *protocol.RawMessage, 64),
		done:     make(chan struct{}),
	}
	go c.readLoop()
	return c, nil
}

// Incoming returns a read-only channel of messages arriving from the server.
func (c *Client) Incoming() <-chan *protocol.RawMessage {
	return c.incoming
}

// Join sends a room.join notification to the server.
func (c *Client) Join(params protocol.JoinParams) error {
	notif := protocol.NewNotification("room.join", params)
	data, err := protocol.EncodeLine(notif)
	if err != nil {
		return err
	}
	_, err = c.conn.Write(data)
	return err
}

// Send sends a room.send notification with the given content and optional mentions.
func (c *Client) Send(content protocol.Content, mentions []string) error {
	params := protocol.SendParams{
		Content:  []protocol.Content{content},
		Mentions: mentions,
	}
	notif := protocol.NewNotification("room.send", params)
	data, err := protocol.EncodeLine(notif)
	if err != nil {
		return err
	}
	_, err = c.conn.Write(data)
	return err
}

// SendStatus sends a room.status notification with the given status string.
// An empty status string means the participant is idle.
func (c *Client) SendStatus(name, status string) error {
	params := protocol.StatusParams{
		Name:   name,
		Status: status,
	}
	notif := protocol.NewNotification("room.status", params)
	data, err := protocol.EncodeLine(notif)
	if err != nil {
		return err
	}
	_, err = c.conn.Write(data)
	return err
}

// Close signals the read loop to stop and closes the underlying connection.
// It is safe to call Close concurrently or more than once.
func (c *Client) Close() error {
	c.closeOnce.Do(func() { close(c.done) })
	return c.conn.Close()
}

// readLoop reads NDJSON lines from the connection, decodes each as a RawMessage,
// and sends it to the incoming channel. It exits when the connection is closed
// or the done channel is closed. It closes the incoming channel on exit so that
// consumers using range will unblock.
func (c *Client) readLoop() {
	defer close(c.incoming)
	sc := bufio.NewScanner(c.conn)
	for sc.Scan() {
		line := sc.Bytes()
		msg, err := protocol.DecodeLine(line)
		if err != nil {
			continue
		}
		select {
		case c.incoming <- msg:
		case <-c.done:
			return
		}
	}
}
