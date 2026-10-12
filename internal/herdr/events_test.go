package herdr

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestEventsSubscribeFirst(t *testing.T) {
	r, w := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer r.Close()
	defer w.Close()
	var reads atomic.Int32
	done := make(chan error, 1)
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	go func() {
		done <- ReadEvents(ctx, r, func() error { reads.Add(1); entered <- struct{}{}; <-release; return nil }, func() error { reads.Add(1); entered <- struct{}{}; return nil })
	}()
	time.Sleep(20 * time.Millisecond)
	if reads.Load() != 0 {
		t.Fatal("read before ack")
	}
	_, _ = io.WriteString(w, `{"id":"parley-requests","result":{"type":"subscription_started"}}`+"\n")
	<-entered
	_, _ = io.WriteString(w, `{"event":"workspace_metadata_updated","data":{"type":"workspace_metadata_updated"}}`+"\n")
	close(release)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("lost wake during read")
	}
	w.Close()
	if e := <-done; e != io.EOF {
		t.Fatal(e)
	}
}
func TestEventsReject(t *testing.T) {
	for _, s := range []string{`{"event":"workspace_metadata_updated"}`, `{"error":{"code":"events_lost"}}`, `{"error":{"code":"invalid_request"}}`, `{bad`} {
		if e := ReadEvents(context.Background(), strings.NewReader(s+"\n"), func() error { return nil }, func() error { return nil }); e == nil {
			t.Fatal(s)
		}
	}
}
func TestEventsChildCancellation(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "ssh")
	if e := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' '{\"id\":\"parley-requests\",\"result\":{\"type\":\"subscription_started\"}}'\nexec sleep 60\n"), 0700); e != nil {
		t.Fatal(e)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	ctx, cancel := context.WithCancel(context.Background())
	key, _ := SessionKey("/tmp/a.sock")
	p := Peer{Machine: Machine{ID: "peer", Target: "host", Session: "agents"}, Binary: "/opt/parley", Config: SessionConfig{SessionKey: key, ControllerID: "local"}}
	c := Client{}
	stream, e := c.OpenEvents(ctx, p)
	if e != nil {
		t.Fatal(e)
	}
	process := stream.(*processStream)
	cancel()
	deadline := time.After(3 * time.Second)
	finished := make(chan error, 1)
	go func() { finished <- stream.Close() }()
	select {
	case <-finished:
	case <-deadline:
		t.Fatal("event child not reaped")
	}
	if process.cmd.ProcessState == nil || !process.cmd.ProcessState.Exited() && process.cmd.ProcessState.String() == "" {
		t.Fatal("missing child state")
	}
}
func TestEventsCallbackFailure(t *testing.T) {
	r, w := io.Pipe()
	defer w.Close()
	defer r.Close()
	done := make(chan error, 1)
	go func() {
		done <- ReadEvents(context.Background(), r, func() error { return fmt.Errorf("queue unavailable") }, func() error { return nil })
	}()
	io.WriteString(w, `{"id":"parley-requests","result":{"type":"subscription_started"}}`+"\n")
	select {
	case e := <-done:
		if e == nil || !strings.Contains(e.Error(), "queue unavailable") {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("callback error blocked reader")
	}
}
func TestEventsReconnectBeforeRead(t *testing.T) {
	dir := t.TempDir()
	count := filepath.Join(dir, "count")
	script := filepath.Join(dir, "ssh")
	body := "#!/bin/sh\nif [ -f '" + count + "' ]; then printf '%s\\n' '{\"id\":\"parley-requests\",\"result\":{\"type\":\"subscription_started\"}}'; exec sleep 60; fi\nprintf x > '" + count + "'\nprintf '%s\\n' '{\"id\":\"parley-requests\",\"result\":{\"type\":\"subscription_started\"}}'\nsleep 0.1\nprintf '%s\\n' '{\"id\":\"parley-requests\",\"error\":{\"code\":\"events_lost\"}}'\n"
	if e := os.WriteFile(script, []byte(body), 0700); e != nil {
		t.Fatal(e)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	key, _ := SessionKey("/tmp/a.sock")
	p := Peer{Machine: Machine{ID: "peer", Target: "host", Session: "agents"}, Binary: "/opt/parley", Config: SessionConfig{SessionKey: key, ControllerID: "local"}}
	var reads atomic.Int32
	c := Client{}
	e := c.WatchRequests(ctx, p, func(context.Context, Peer) error {
		if reads.Add(1) == 2 {
			cancel()
		}
		return nil
	})
	if e != context.Canceled || reads.Load() != 2 {
		t.Fatal(e, reads.Load())
	}
}
