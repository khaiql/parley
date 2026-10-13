package herdr

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func peer() Peer {
	k, _ := SessionKey("/private/herdr.sock")
	return Peer{Machine: Machine{ID: "local"}, Config: SessionConfig{MachineID: "local", HerdrSocket: "/private/herdr.sock", SessionKey: k}}
}
func TestDecodeRequest(t *testing.T) {
	r := request("one")
	data, _ := json.Marshal(r)
	got, err := DecodeRequest(strings.NewReader(string(data)), peer())
	if err != nil || got.Target.MachineID != "local" || got.Target.HerdrSocket != "/private/herdr.sock" {
		t.Fatal(got, err)
	}
	bad := invalidRequests(t)
	for i, b := range bad {
		if _, err := DecodeRequest(strings.NewReader(b), peer()); err == nil {
			t.Errorf("accepted bad request %d", i)
		}
	}
}
func startLocal(t *testing.T, path string, dispatch func(context.Context, Peer, Request) (Response, error)) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ServeLocal(ctx, path, peer(), dispatch) }()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if info, err := os.Lstat(path); err == nil && info.Mode().Perm() == 0600 {
			return cancel, done
		}
		select {
		case err := <-done:
			t.Fatalf("serve: %v", err)
		default:
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	t.Fatal("not ready")
	return nil, nil
}
func TestLocalIPC(t *testing.T) {
	dir, err := os.MkdirTemp("", "h-ipc-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "s")
	var count atomic.Int32
	cancel, done := startLocal(t, path, func(ctx context.Context, p Peer, r Request) (Response, error) {
		count.Add(1)
		if p.Config.SessionKey != peer().Config.SessionKey || r.Target.MachineID != "local" {
			t.Error("origin not normalized")
		}
		return Response{Status: "ok"}, nil
	})
	defer cancel()
	r := request("one")
	r.Op = "status"
	r.RoomID = ""
	resp, err := CallLocal(context.Background(), path, r)
	if err != nil || resp.Status != "ok" {
		t.Fatal(resp, err)
	}
	r.Op = "join"
	resp, err = CallLocal(context.Background(), path, r)
	if err != nil || resp.Code != "invalid_request" {
		t.Fatal(resp, err)
	}
	if count.Load() != 1 {
		t.Fatal("invalid request dispatched")
	}
	info, _ := os.Lstat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatalf("socket mode %o", info.Mode().Perm())
	}
	if err := ServeLocal(context.Background(), path, peer(), nil); err == nil {
		t.Fatal("live socket replaced")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown stuck")
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("socket not removed")
	}
	os.Symlink(filepath.Join(dir, "victim"), path)
	if err := ServeLocal(context.Background(), path, peer(), nil); err == nil {
		t.Fatal("symlink socket accepted")
	}
}
func TestLocalInvalidDecodeNeverDispatches(t *testing.T) {
	dir, _ := os.MkdirTemp("", "h-ipc-")
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "s")
	var count atomic.Int32
	cancel, done := startLocal(t, path, func(context.Context, Peer, Request) (Response, error) {
		count.Add(1)
		return Response{Status: "ok"}, nil
	})
	defer func() { cancel(); <-done }()
	for _, body := range invalidRequests(t) {
		c, err := net.Dial("unix", path)
		if err != nil {
			t.Fatal(err)
		}
		c.SetDeadline(time.Now().Add(time.Second))
		c.Write([]byte(body))
		c.(*net.UnixConn).CloseWrite()
		var wire struct {
			Response Response `json:"response"`
		}
		if err := json.NewDecoder(c).Decode(&wire); err != nil {
			t.Fatal(err)
		}
		c.Close()
		if wire.Response.Code != "invalid_request" {
			t.Fatal(wire)
		}
	}
	if count.Load() != 0 {
		t.Fatal("rejected request dispatched")
	}
}

func TestLocalOperationCanTakeLongerThanRequestReadDeadline(t *testing.T) {
	t.Parallel()
	dir, err := os.MkdirTemp("", "h-ipc-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "s")
	cancel, done := startLocal(t, path, func(ctx context.Context, _ Peer, _ Request) (Response, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) < 29*time.Second {
			t.Error("operation deadline too short")
		}
		select {
		case <-time.After(5100 * time.Millisecond):
			return Response{Status: "ok"}, nil
		case <-ctx.Done():
			return Response{}, ctx.Err()
		}
	})
	defer func() { cancel(); <-done }()
	r := request("one")
	resp, err := CallLocal(context.Background(), path, r)
	if err != nil || resp.Status != "ok" {
		t.Fatal(resp, err)
	}
}
func TestLocalSessionHandshakeAndCancellation(t *testing.T) {
	dir, err := os.MkdirTemp("", "h-ipc-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "s")
	entered := make(chan struct{})
	cancel, done := startLocal(t, path, func(ctx context.Context, _ Peer, _ Request) (Response, error) {
		close(entered)
		<-ctx.Done()
		return Response{}, ctx.Err()
	})
	r := request("one")
	r.Target.HerdrSocket = "/other/herdr.sock"
	if _, err := CallLocal(context.Background(), path, r); err == nil || !strings.Contains(err.Error(), "session_mismatch") {
		t.Fatal(err)
	}
	r = request("one")
	type callResult struct {
		response Response
		err      error
	}
	result := make(chan callResult, 1)
	go func() { resp, err := CallLocal(context.Background(), path, r); result <- callResult{resp, err} }()
	<-entered
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown stuck")
	}
	select {
	case result := <-result:
		if result.err == nil && result.response.Code != "operation_uncertain" {
			t.Fatalf("cancelled mutation returned %v", result.response)
		}
	case <-time.After(time.Second):
		t.Fatal("call did not stop")
	}
}

func TestDecodeRequestSizeAndTopicBoundaries(t *testing.T) {
	r := request("one")
	r.Op = "start"
	r.RoomID = ""
	r.Topic = strings.Repeat("t", 512)
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	body := string(b) + strings.Repeat(" ", 65536-len(b))
	if _, err := DecodeRequest(strings.NewReader(body), peer()); err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeRequest(strings.NewReader(body+" "), peer()); err == nil {
		t.Fatal("65,537 bytes accepted")
	}
	r.Topic += "t"
	b, _ = json.Marshal(r)
	if _, err := DecodeRequest(strings.NewReader(string(b)), peer()); err == nil {
		t.Fatal("513-byte topic accepted")
	}
}
func TestLocalReadDeadline(t *testing.T) {
	t.Parallel()
	dir, err := os.MkdirTemp("", "h-ipc-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "s")
	var count atomic.Int32
	cancel, done := startLocal(t, path, func(context.Context, Peer, Request) (Response, error) {
		count.Add(1)
		return Response{Status: "ok"}, nil
	})
	defer func() { cancel(); <-done }()
	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(6 * time.Second))
	c.Write([]byte(`{"id":`))
	var wire localResponse
	if err := json.NewDecoder(c).Decode(&wire); err != nil {
		t.Fatal(err)
	}
	if wire.Response.Code != "invalid_request" || count.Load() != 0 {
		t.Fatal(wire, count.Load())
	}
}
func TestLocalRejectsSharedRuntimeDirectory(t *testing.T) {
	dir, err := os.MkdirTemp("", "h-ipc-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := ServeLocal(context.Background(), filepath.Join(dir, "s"), peer(), func(context.Context, Peer, Request) (Response, error) { return Response{}, nil }); err == nil {
		t.Fatal("shared runtime directory accepted")
	}
	info, _ := os.Stat(dir)
	if info.Mode().Perm() != 0755 {
		t.Fatal("shared directory mode changed")
	}
}

func invalidRequests(t *testing.T) []string {
	t.Helper()
	data, err := json.Marshal(request("one"))
	if err != nil {
		t.Fatal(err)
	}
	bad := []string{`{"id":"one","op":"status","unknown":1}`, `{"id":"one","op":"exec"}`, string(data) + string(data), string(data) + "\xff", strings.Repeat(" ", 65537), `null`}
	for _, mutate := range []func(*Request){func(r *Request) { r.Topic = strings.Repeat("t", 513) }, func(r *Request) { r.ID = strings.Repeat("a", 129) }, func(r *Request) { r.ID = "../x" }, func(r *Request) { r.Target.MachineID = "forged" }, func(r *Request) { r.Target.HerdrSocket = "/forged.sock" }, func(r *Request) { r.RoomID = "" }, func(r *Request) { r.Target.Kind = "shell" }} {
		r := request("one")
		mutate(&r)
		b, _ := json.Marshal(r)
		bad = append(bad, string(b))
	}
	return bad
}
