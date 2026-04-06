package server

import "sync"

// ClientConn represents a connected participant's network connection.
type ClientConn struct {
	Name string
	Send chan []byte
	Done chan struct{}
}

// ConnectionManager tracks active client connections and provides
// thread-safe broadcast capabilities.
type ConnectionManager struct {
	mu    sync.RWMutex
	conns map[string]*ClientConn
}

// NewConnectionManager returns an initialized ConnectionManager.
func NewConnectionManager() *ConnectionManager {
	return &ConnectionManager{
		conns: make(map[string]*ClientConn),
	}
}

// Add registers a client connection under the given name.
func (cm *ConnectionManager) Add(name string, cc *ClientConn) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.conns[name] = cc
}

// Remove closes the client's Done channel and deletes it from the map.
func (cm *ConnectionManager) Remove(name string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	if cc, ok := cm.conns[name]; ok {
		close(cc.Done)
		delete(cm.conns, name)
	}
}

// Broadcast sends data to every connected client's Send channel.
// If a client's buffer is full the message is dropped (non-blocking).
func (cm *ConnectionManager) Broadcast(data []byte) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	for _, cc := range cm.conns {
		select {
		case cc.Send <- data:
		default:
		}
	}
}

// BroadcastExcept sends data to all connected clients except the named one.
// If a client's buffer is full the message is dropped (non-blocking).
func (cm *ConnectionManager) BroadcastExcept(name string, data []byte) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	for n, cc := range cm.conns {
		if n == name {
			continue
		}
		select {
		case cc.Send <- data:
		default:
		}
	}
}
