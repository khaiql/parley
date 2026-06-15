package adapter

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"

	"github.com/khaiql/parley/internal/model"
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
	if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
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
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return ControlResponse{}, err
	}
	defer conn.Close()

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
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&req); err != nil {
		resp.Error = err.Error()
	} else {
		resp = handler(req)
	}
	_ = json.NewEncoder(conn).Encode(resp)
}
