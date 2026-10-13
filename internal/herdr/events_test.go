package herdr

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
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
func TestEventsSSHDiagnostic(t *testing.T) {
	dir := t.TempDir()
	if e := os.WriteFile(filepath.Join(dir, "ssh"), []byte("#!/bin/sh\nprintf '%s\\n' 'fixture SSH refused' >&2\nexit 255\n"), 0700); e != nil {
		t.Fatal(e)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	key, _ := SessionKey("/tmp/a.sock")
	p := Peer{Machine: Machine{ID: "peer", Target: "host", Session: "agents"}, Binary: "/opt/parley", Config: SessionConfig{SessionKey: key, ControllerID: "local"}}
	c := Client{}
	stream, e := c.OpenEvents(context.Background(), p)
	if e != nil {
		t.Fatal(e)
	}
	defer stream.Close()
	_, e = io.ReadAll(stream)
	if !errors.Is(e, errEventProcess) || !strings.Contains(e.Error(), "fixture SSH refused") {
		t.Fatal(e)
	}
}

type observedLossReader struct {
	io.ReadCloser
	lost chan struct{}
	once sync.Once
}

func (r *observedLossReader) Read(p []byte) (int, error) {
	n, e := r.ReadCloser.Read(p)
	if e == io.EOF || strings.Contains(string(p[:n]), "events_lost") {
		r.once.Do(func() { close(r.lost) })
	}
	return n, e
}
func TestEventsLossWhileReadInFlight(t *testing.T) {
	for _, loss := range []string{"EOF", "events_lost"} {
		for _, callback := range []string{"canceled", "failure"} {
			t.Run(loss+"/"+callback, func(t *testing.T) {
				reader, writer := io.Pipe()
				defer reader.Close()
				defer writer.Close()
				seen := make(chan struct{})
				stream := &observedLossReader{ReadCloser: reader, lost: seen}
				entered := make(chan struct{})
				done := make(chan error, 1)
				go func() {
					done <- ReadEvents(context.Background(), stream, func() error {
						close(entered)
						<-seen
						if callback == "canceled" {
							return context.Canceled
						}
						return fmt.Errorf("genuine queue failure")
					}, func() error { return nil })
				}()
				io.WriteString(writer, `{"id":"parley-requests","result":{"type":"subscription_started"}}`+"\n")
				<-entered
				if loss == "EOF" {
					writer.Close()
				} else {
					io.WriteString(writer, `{"id":"parley-requests","error":{"code":"events_lost"}}`+"\n")
				}
				select {
				case e := <-done:
					if callback == "failure" {
						if e == nil || !strings.Contains(e.Error(), "genuine queue failure") {
							t.Fatal(e)
						}
					} else if loss == "EOF" {
						if e != io.EOF {
							t.Fatal(e)
						}
					} else {
						var api *apiError
						if !errors.As(e, &api) || api.Code != "events_lost" {
							t.Fatal(e)
						}
					}
				case <-time.After(time.Second):
					t.Fatal("blocked loss while reading")
				}
			})
		}
	}
}

func TestEventTransportErrorClassification(t *testing.T) {
	for _, cause := range []error{syscall.ENOENT, syscall.ECONNREFUSED, syscall.ECONNRESET} {
		err := fmt.Errorf("relay: %w", &net.OpError{Op: "dial", Net: "unix", Err: cause})
		if !isEventTransportError(err) || EventFailureCode(err) != "helper_transport_failed" {
			t.Fatal(err)
		}
		if isEventTransportError(errors.New(err.Error())) {
			t.Fatal("classified transport by error text")
		}
	}
	if !isEventTransportError(fmt.Errorf("relay: %w", ErrHostUnavailable)) {
		t.Fatal("typed host outage is permanent")
	}
	for _, frame := range []string{
		`{bad`,
		`{"id":"wrong-session","result":{"type":"subscription_started"}}`,
		`{"error":{"code":"capability_unavailable","message":"connect: connection refused"}}`,
		`{"error":{"code":"session_mismatch"}}`,
		`{"error":{"code":"controller_conflict"}}`,
		`{"error":{"code":"helper_startup_failed"}}`,
	} {
		var reads atomic.Int32
		read := func() error { reads.Add(1); return fmt.Errorf("unexpected durable read") }
		err := ReadEvents(context.Background(), strings.NewReader(frame+"\n"), read, read)
		if err == nil || isEventTransportError(err) || reads.Load() != 0 {
			t.Fatalf("permanent frame %s: %v", frame, err)
		}
	}
}

const testEventAck = `{"id":"parley-requests","result":{"type":"subscription_started"}}` + "\n"
const testKeepalive = `{"id":"parley-requests","result":{"type":"parley_transport_keepalive"}}` + "\n"

func TestEventsKeepaliveNoWake(t *testing.T) {
	r, w := io.Pipe()
	defer w.Close()
	defer r.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	var wakes atomic.Int32
	done := make(chan error, 1)
	go func() {
		done <- ReadEvents(ctx, r, func() error { close(started); return nil }, func() error { wakes.Add(1); return nil })
	}()
	io.WriteString(w, testEventAck)
	<-started
	io.WriteString(w, testKeepalive)
	io.WriteString(w, testKeepalive)
	cancel()
	if e := <-done; e != context.Canceled {
		t.Fatal(e)
	}
	if wakes.Load() != 0 {
		t.Fatal("transport frame woke queue")
	}
}

func TestEventsKeepaliveReject(t *testing.T) {
	for _, frame := range []string{
		testKeepalive,
		testEventAck + strings.Replace(testKeepalive, "parley-requests", "wrong", 1),
		testEventAck + strings.Replace(testKeepalive, "parley_transport_keepalive", "unknown", 1),
		testEventAck + `{"id":"parley-requests","result":{"type":"parley_transport_keepalive","extra":true}}` + "\n",
		testEventAck + `{"id":"parley-requests","result":{"type":"parley_transport_keepalive"},"event":"workspace_metadata_updated"}` + "\n",
		testEventAck + `{"id":"parley-requests","result":{"type":` + "\n",
	} {
		e := ReadEvents(context.Background(), strings.NewReader(frame), func() error { return nil }, func() error { return nil })
		if e == nil || isEventTransportError(e) {
			t.Fatalf("accepted invalid control: %q: %v", frame, e)
		}
	}
}

func TestRelayEventsQuietLifecycle(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"quiet-keepalive", "broken-output", "cancel", "pre-ack"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			dir, e := os.MkdirTemp("/tmp", "qe-")
			if e != nil {
				t.Fatal(e)
			}
			defer os.RemoveAll(dir)
			socket := filepath.Join(dir, "events.sock")
			listener, e := net.Listen("unix", socket)
			if e != nil {
				t.Fatal(e)
			}
			defer listener.Close()
			nativeClosed := make(chan struct{})
			ack := make(chan struct{})
			go func() {
				conn, e := listener.Accept()
				if e != nil {
					return
				}
				defer conn.Close()
				defer close(nativeClosed)
				b := make([]byte, 4096)
				conn.Read(b)
				if mode != "pre-ack" {
					io.WriteString(conn, testEventAck)
				}
				close(ack)
				io.Copy(io.Discard, conn)
			}()
			r, w := io.Pipe()
			defer r.Close()
			defer w.Close()
			deadline := time.AfterFunc(7*time.Second, func() { r.Close() })
			defer deadline.Stop()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- RelayEvents(ctx, socket, w) }()
			<-ack
			if mode == "pre-ack" {
				unexpected := make(chan bool, 1)
				go func() { b := make([]byte, 1); n, _ := r.Read(b); unexpected <- n != 0 }()
				select {
				case <-unexpected:
					t.Fatal("output before native acknowledgement")
				case <-time.After(2100 * time.Millisecond):
				}
				cancel()
				w.Close()
				if <-unexpected {
					t.Fatal("output before native acknowledgement")
				}
			} else {
				scanner := bufio.NewScanner(r)
				if !scanner.Scan() || scanner.Text() != strings.TrimSpace(testEventAck) {
					t.Fatal("missing first native ack")
				}
				if mode == "cancel" {
					cancel()
				} else if mode == "broken-output" {
					r.Close()
				} else {
					if !scanner.Scan() || scanner.Text() != strings.TrimSpace(testKeepalive) {
						t.Fatal("missing quiet keepalive")
					}
					cancel()
				}
			}
			select {
			case e := <-done:
				if e == nil {
					t.Fatal("missing terminal error")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("relay stayed alive")
			}
			select {
			case <-nativeClosed:
			case <-time.After(time.Second):
				t.Fatal("native reader stayed alive")
			}
		})
	}
}

func TestRelayEventsRejectInvalidAcknowledgement(t *testing.T) {
	for _, frame := range []string{testKeepalive, strings.Replace(testEventAck, "parley-requests", "wrong", 1), "{bad\n", string([]byte{0xff, '\n'}), strings.Repeat("x", 1024*1024) + "\n"} {
		dir, e := os.MkdirTemp("/tmp", "qr-")
		if e != nil {
			t.Fatal(e)
		}
		socket := filepath.Join(dir, "events.sock")
		listener, e := net.Listen("unix", socket)
		if e != nil {
			t.Fatal(e)
		}
		closed := make(chan struct{})
		go func() {
			defer close(closed)
			conn, e := listener.Accept()
			if e != nil {
				return
			}
			defer conn.Close()
			b := make([]byte, 4096)
			conn.Read(b)
			io.WriteString(conn, frame)
			io.Copy(io.Discard, conn)
		}()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		var out bytes.Buffer
		err := RelayEvents(ctx, socket, &out)
		cancel()
		listener.Close()
		<-closed
		os.RemoveAll(dir)
		if err == nil || isEventTransportError(err) || out.Len() != 0 {
			t.Fatalf("invalid acknowledgement accepted: %v, output bytes %d", err, out.Len())
		}
	}
}
