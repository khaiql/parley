package herdr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
)

const requestLimit = 64 * 1024

func safeID(id string) error {
	if id == "" || len(id) > 128 || id == "." || id == ".." {
		return fmt.Errorf("invalid id")
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.' || c == ':') {
			return fmt.Errorf("invalid id")
		}
	}
	return nil
}
func validateRequest(req Request) error {
	if err := safeID(req.ID); err != nil {
		return err
	}
	if len(req.Topic) > 512 || !utf8.ValidString(req.Topic) {
		return fmt.Errorf("invalid topic")
	}
	if req.RoomID != "" {
		if err := safeID(req.RoomID); err != nil {
			return err
		}
	}
	switch req.Op {
	case "start":
		if req.RoomID != "" {
			return fmt.Errorf("start must not submit a room id")
		}
	case "join":
		if req.RoomID == "" || req.Topic != "" {
			return fmt.Errorf("join requires room id and no topic")
		}
	case "status":
		if req.Topic != "" {
			return fmt.Errorf("status must not submit a topic")
		}
	default:
		return fmt.Errorf("unsupported operation")
	}
	if req.Target.HerdrSocket != "" {
		if _, err := SessionKey(req.Target.HerdrSocket); err != nil {
			return err
		}
	}
	if req.Target.MachineID != "" {
		if err := safeID(req.Target.MachineID); err != nil {
			return err
		}
	}
	if req.Op != "status" {
		if req.Target.HerdrSocket == "" || req.Target.MachineID == "" {
			return fmt.Errorf("target origin required")
		}
		if req.Target.Kind != "codex" && req.Target.Kind != "claude" {
			return fmt.Errorf("unsupported agent kind")
		}
		if req.Target.TerminalID == "" || req.Target.NativeSession == "" {
			return fmt.Errorf("agent identity required")
		}
	}
	for _, value := range []string{req.Target.WorkspaceID, req.Target.PaneID, req.Target.TerminalID, req.Target.NativeSession} {
		if value != "" {
			if err := safeID(value); err != nil {
				return err
			}
		}
	}
	return nil
}
func trustedOrigin(origin Peer) (string, string, error) {
	key, err := SessionKey(origin.Config.HerdrSocket)
	if err != nil {
		return "", "", err
	}
	if key != origin.Config.SessionKey {
		return "", "", fmt.Errorf("trusted session key mismatch")
	}
	id := origin.Config.MachineID
	if id == "" {
		id = origin.Machine.ID
	}
	if id == "" || id == "self" {
		return "", "", fmt.Errorf("trusted origin requires normalized machine id")
	}
	if origin.Machine.ID != "" && origin.Machine.ID != id {
		return "", "", fmt.Errorf("trusted machine mismatch")
	}
	if err := safeID(id); err != nil {
		return "", "", err
	}
	return id, key, nil
}
func DecodeRequest(r io.Reader, origin Peer) (Request, error) {
	var req Request
	b, err := io.ReadAll(io.LimitReader(r, requestLimit+1))
	if err != nil {
		return req, err
	}
	if len(b) > requestLimit || !utf8.Valid(b) {
		return req, fmt.Errorf("invalid request size or UTF-8")
	}
	if len(bytes.TrimSpace(b)) == 0 || bytes.TrimSpace(b)[0] != '{' {
		return req, fmt.Errorf("request must be an object")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(&req); err != nil {
		return req, err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return req, fmt.Errorf("request must contain exactly one object")
	}
	id, _, err := trustedOrigin(origin)
	if err != nil {
		return Request{}, err
	}
	if req.Target.MachineID != "" && req.Target.MachineID != "self" && req.Target.MachineID != id {
		return Request{}, fmt.Errorf("forged origin machine")
	}
	if req.Target.HerdrSocket != "" && req.Target.HerdrSocket != origin.Config.HerdrSocket {
		return Request{}, fmt.Errorf("forged origin socket")
	}
	req.Target.MachineID = id
	req.Target.HerdrSocket = origin.Config.HerdrSocket
	if err := validateRequest(req); err != nil {
		return Request{}, err
	}
	return req, nil
}

type localResponse struct {
	SessionKey string   `json:"session_key"`
	Response   Response `json:"response"`
}

func privateSocketDir(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fmt.Errorf("socket path must be absolute and clean")
	}
	dir := filepath.Dir(path)
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || !ok || int64(stat.Uid) != int64(os.Getuid()) || info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("socket directory must be private and owned by current user")
	}
	return nil
}
func ServeLocal(ctx context.Context, path string, origin Peer, dispatch func(context.Context, Peer, Request) (Response, error)) error {
	_, key, err := trustedOrigin(origin)
	if err != nil {
		return err
	}
	if err := privateSocketDir(path); err != nil {
		return err
	}
	// A lifetime lock serializes startup and stale-socket cleanup, including racing servers.
	acquire, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	return lockFile(acquire, path+".lock", func() error {
		if info, err := os.Lstat(path); err == nil {
			if info.Mode()&os.ModeSocket == 0 {
				return fmt.Errorf("socket path is not a socket")
			}
			c, err := net.DialTimeout("unix", path, 100*time.Millisecond)
			if err == nil {
				c.Close()
				return fmt.Errorf("socket already active")
			}
			if !errors.Is(err, syscall.ECONNREFUSED) && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if err := os.Remove(path); err != nil {
				return err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
		if err != nil {
			return err
		}
		listener.SetUnlinkOnClose(false)
		defer listener.Close()
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		defer func() {
			current, err := os.Lstat(path)
			if err == nil && os.SameFile(info, current) {
				_ = os.Remove(path)
			}
		}()
		if err := os.Chmod(path, 0600); err != nil {
			return err
		}
		stopped := make(chan struct{})
		defer close(stopped)
		go func() {
			select {
			case <-ctx.Done():
				listener.Close()
			case <-stopped:
			}
		}()
		var handlers sync.WaitGroup
		defer handlers.Wait()
		for {
			conn, err := listener.AcceptUnix()
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
			handlers.Go(func() { serveLocalConn(ctx, conn, key, origin, dispatch) })
		}
	})
}
func serveLocalConn(ctx context.Context, conn *net.UnixConn, key string, origin Peer, dispatch func(context.Context, Peer, Request) (Response, error)) {
	defer conn.Close()
	closed := make(chan struct{})
	defer close(closed)
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-closed:
		}
	}()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	req, err := DecodeRequest(conn, origin)
	resp := Response{Status: "error", Code: "invalid_request"}
	if err != nil {
		resp.Message = err.Error()
	} else {
		operation, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		result := make(chan localResponse, 1)
		go func() {
			r, err := dispatch(operation, origin, req)
			if err != nil {
				code := "operation_failed"
				if req.Op != "status" {
					code = "operation_uncertain"
				}
				r = Response{Status: "error", Code: code, Message: err.Error()}
			}
			result <- localResponse{Response: r}
		}()
		select {
		case r := <-result:
			resp = r.Response
		case <-operation.Done():
			code := "operation_timeout"
			if req.Op != "status" {
				code = "operation_uncertain"
			}
			resp = Response{Status: "error", Code: code, Message: operation.Err().Error()}
		}
	}
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_ = json.NewEncoder(conn).Encode(localResponse{SessionKey: key, Response: resp})
}
func CallLocal(ctx context.Context, path string, req Request) (Response, error) {
	key, err := SessionKey(req.Target.HerdrSocket)
	if err != nil {
		return Response{}, err
	}
	if err := privateSocketDir(path); err != nil {
		return Response{}, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return Response{}, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if info.Mode()&os.ModeSocket == 0 || !ok || int64(stat.Uid) != int64(os.Getuid()) || info.Mode().Perm()&0077 != 0 {
		return Response{}, fmt.Errorf("unsafe local socket")
	}
	dialer := net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(ctx, "unix", path)
	if err != nil {
		return Response{}, err
	}
	defer conn.Close()
	closed := make(chan struct{})
	defer close(closed)
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-closed:
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := conn.SetWriteDeadline(deadline); err != nil {
		return Response{}, err
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return Response{}, err
	}
	if err := conn.(*net.UnixConn).CloseWrite(); err != nil {
		return Response{}, err
	}
	deadline = time.Now().Add(35 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := conn.SetReadDeadline(deadline); err != nil {
		return Response{}, err
	}
	var wire localResponse
	d := json.NewDecoder(io.LimitReader(conn, requestLimit+1))
	d.DisallowUnknownFields()
	if err := d.Decode(&wire); err != nil {
		return Response{}, err
	}
	if wire.SessionKey != key {
		return Response{}, fmt.Errorf("session_mismatch")
	}
	return wire.Response, nil
}
