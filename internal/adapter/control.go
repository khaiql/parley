package adapter

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/khaiql/parley/internal/model"
)

const (
	controlDefaultTimeout = 5 * time.Second
	controlTimeoutGrace   = 50 * time.Millisecond
)

type ControlRequest struct {
	Type    string `json:"type"`
	Text    string `json:"text,omitempty"`
	Peek    bool   `json:"peek,omitempty"`
	Limit   int    `json:"limit,omitempty"`
	Timeout string `json:"timeout,omitempty"`
}

type ControlResponse struct {
	OK     bool          `json:"ok"`
	Status string        `json:"status,omitempty"`
	Events []model.Event `json:"events,omitempty"`
	Error  string        `json:"error,omitempty"`
}

func ServeControl(socketPath string, handler func(ControlRequest) ControlResponse) error {
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o700); err != nil {
		return err
	}
	if err := removeStaleControlSocket(socketPath); err != nil {
		return err
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return err
	}
	defer listener.Close()

	for {
		conn, err := listener.Accept()
		if err != nil {
			return err
		}
		go serveControlConn(conn, handler)
	}
}

func CallControl(socketPath string, req ControlRequest) (ControlResponse, error) {
	dialer := net.Dialer{Timeout: controlDefaultTimeout}
	conn, err := dialer.Dial("unix", socketPath)
	if err != nil {
		return ControlResponse{}, err
	}
	defer conn.Close()

	if err := conn.SetDeadline(time.Now().Add(controlCallTimeout(req))); err != nil {
		return ControlResponse{}, err
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return ControlResponse{}, err
	}
	var resp ControlResponse
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&resp); err != nil {
		return ControlResponse{}, err
	}
	return resp, nil
}

func serveControlConn(conn net.Conn, handler func(ControlRequest) ControlResponse) {
	defer conn.Close()

	var req ControlRequest
	resp := ControlResponse{OK: false}
	_ = conn.SetReadDeadline(time.Now().Add(controlDefaultTimeout))
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&req); err != nil {
		resp.Error = err.Error()
	} else {
		_ = conn.SetReadDeadline(time.Time{})
		resp = handler(req)
	}
	_ = conn.SetWriteDeadline(time.Now().Add(controlDefaultTimeout))
	_ = json.NewEncoder(conn).Encode(resp)
}

func removeStaleControlSocket(socketPath string) error {
	info, err := os.Lstat(socketPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("control socket path exists and is not a socket: %s", socketPath)
	}

	conn, err := net.DialTimeout("unix", socketPath, controlDefaultTimeout)
	if err == nil {
		_ = conn.Close()
		return fmt.Errorf("control socket already active: %s", socketPath)
	}
	return os.Remove(socketPath)
}

func controlCallTimeout(req ControlRequest) time.Duration {
	if req.Timeout == "" {
		return controlDefaultTimeout
	}
	timeout, err := time.ParseDuration(req.Timeout)
	if err != nil || timeout < 0 {
		return controlDefaultTimeout
	}
	return timeout + controlTimeoutGrace
}
