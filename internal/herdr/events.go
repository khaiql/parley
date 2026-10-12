package herdr

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

func subscriptionParams() any {
	return map[string]any{"subscriptions": []map[string]string{{"type": "workspace.metadata_updated"}}}
}
func subscriptionRequest() any {
	return map[string]any{"id": "parley-requests", "method": "events.subscribe", "params": subscriptionParams()}
}

// ReadEvents drains invalidations independently of slow authoritative queue reads.
func ReadEvents(ctx context.Context, stream io.Reader, started func() error, wake func() error) error {
	ctx, cancel := context.WithCancel(ctx)
	if closer, ok := stream.(io.Closer); ok {
		stopped := make(chan struct{})
		defer close(stopped)
		go func() {
			select {
			case <-ctx.Done():
				closer.Close()
			case <-stopped:
			}
		}()
	}
	ticks := make(chan bool, 1)
	errors := make(chan error, 1)
	var worker sync.WaitGroup
	worker.Add(1)
	defer func() { cancel(); worker.Wait() }()
	go func() {
		defer worker.Done()
		first := true
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticks:
				var e error
				if first {
					first = false
					e = started()
				} else {
					e = wake()
				}
				if e != nil {
					select {
					case errors <- e:
					default:
					}
					cancel()
					return
				}
			}
		}
	}()
	s := bufio.NewScanner(stream)
	s.Buffer(make([]byte, 4096), 1024*1024)
	acked := false
	for s.Scan() {
		if e := ctx.Err(); e != nil {
			select {
			case e := <-errors:
				return e
			default:
				return e
			}
		}
		if !utf8.Valid(s.Bytes()) {
			return fmt.Errorf("malformed event frame: invalid UTF-8")
		}
		var f struct {
			ID     string `json:"id"`
			Result *struct {
				Type string `json:"type"`
			} `json:"result"`
			Event string `json:"event"`
			Data  struct {
				Type string `json:"type"`
			} `json:"data"`
			Error *apiError `json:"error"`
		}
		if e := json.Unmarshal(s.Bytes(), &f); e != nil {
			return fmt.Errorf("malformed event frame: %w", e)
		}
		if f.Error != nil {
			if f.Error.Code == "events_lost" {
				return f.Error
			}
			return fmt.Errorf("capability_unavailable: %w", f.Error)
		}
		if !acked {
			if f.ID != "parley-requests" || f.Result == nil || f.Result.Type != "subscription_started" {
				return fmt.Errorf("subscription acknowledgement required")
			}
			acked = true
			select {
			case ticks <- true:
			default:
			}
			continue
		}
		if f.Result != nil {
			return fmt.Errorf("unexpected subscription response")
		}
		if f.Event == "workspace_metadata_updated" {
			if f.Data.Type != f.Event {
				return fmt.Errorf("malformed metadata event")
			}
			select {
			case ticks <- false:
			default:
			}
		} else {
			return fmt.Errorf("malformed event frame: unexpected event")
		}
	}
	select {
	case e := <-errors:
		return e
	default:
	}
	if e := s.Err(); e != nil {
		return e
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return io.EOF
}
func RelayEvents(ctx context.Context, socket string, out io.Writer) error {
	conn, e := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", socket)
	if e != nil {
		return e
	}
	defer conn.Close()
	stopped := make(chan struct{})
	defer close(stopped)
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-stopped:
		}
	}()
	if e = conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); e != nil {
		return e
	}
	if e = json.NewEncoder(conn).Encode(subscriptionRequest()); e != nil {
		return e
	}
	s := bufio.NewScanner(conn)
	s.Buffer(make([]byte, 4096), 1024*1024)
	acked := false
	for s.Scan() {
		if !utf8.Valid(s.Bytes()) {
			return fmt.Errorf("malformed event frame: invalid UTF-8")
		}
		var f struct {
			ID     string `json:"id"`
			Result struct {
				Type string `json:"type"`
			} `json:"result"`
			Error *apiError `json:"error"`
		}
		if e = json.Unmarshal(s.Bytes(), &f); e != nil {
			return fmt.Errorf("malformed event frame: %w", e)
		}
		if !acked && f.Error == nil {
			if f.ID != "parley-requests" || f.Result.Type != "subscription_started" {
				return fmt.Errorf("subscription acknowledgement required")
			}
			acked = true
		}
		if _, e = out.Write(append(append([]byte(nil), s.Bytes()...), '\n')); e != nil {
			return e
		}
		if f.Error != nil {
			return f.Error
		}
	}
	if e = s.Err(); e != nil {
		return e
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return io.EOF
}

var errEventProcess = errors.New("event process failed")

type processStream struct {
	io.ReadCloser
	cancel context.CancelFunc
	cmd    *exec.Cmd
	stderr *boundedBuffer
	once   sync.Once
	err    error
}

func (p *processStream) Close() error {
	p.once.Do(func() { p.cancel(); p.ReadCloser.Close(); p.err = p.cmd.Wait() })
	return p.err
}
func (p *processStream) Read(b []byte) (int, error) {
	n, e := p.ReadCloser.Read(b)
	if e == io.EOF {
		wait := p.Close()
		if wait != nil {
			e = fmt.Errorf("%w: %v", errEventProcess, wait)
		}
	}
	return n, e
}
func (c *Client) OpenEvents(ctx context.Context, p Peer) (io.ReadCloser, error) {
	r := RPCRequest{Operation: "events", Session: p.Machine.Session, SessionKey: p.Config.SessionKey, ControllerID: p.Config.ControllerID}
	if e := r.Validate(); e != nil {
		return nil, e
	}
	if !filepath.IsAbs(p.Binary) || filepath.Clean(p.Binary) != p.Binary || strings.ContainsRune(p.Binary, 0) {
		return nil, fmt.Errorf("invalid installed helper")
	}
	if e := validateMachine(p.Machine); e != nil {
		return nil, e
	}
	ctx, cancel := context.WithCancel(ctx)
	b, e := json.Marshal(r)
	if e != nil {
		cancel()
		return nil, e
	}
	cmd := exec.CommandContext(ctx, "ssh", "-T", "-o", "BatchMode=yes", "-o", "ClearAllForwardings=yes", "--", p.Machine.Target, helperCommand(p.Binary))
	cmd.Stdin = bytes.NewReader(b)
	stderr := &boundedBuffer{limit: 64 * 1024, cancel: cancel}
	cmd.Stderr = stderr
	cmd.WaitDelay = processWaitDelay
	out, e := cmd.StdoutPipe()
	if e != nil {
		cancel()
		return nil, e
	}
	if e = cmd.Start(); e != nil {
		out.Close()
		cancel()
		return nil, e
	}
	stream := &processStream{ReadCloser: out, cancel: cancel, cmd: cmd, stderr: stderr}
	go func() { <-ctx.Done(); stream.Close() }()
	return stream, nil
}

// WatchRequests always obtains a fresh acknowledgement before startup/recovery reads.
func (c *Client) WatchRequests(ctx context.Context, p Peer, reconcile func(context.Context, Peer) error) error {
	backoff := time.Second
	for {
		if e := ctx.Err(); e != nil {
			return e
		}
		stream, e := c.OpenEvents(ctx, p)
		if e == nil {
			e = ReadEvents(ctx, stream, func() error { backoff = time.Second; return reconcile(ctx, p) }, func() error { return reconcile(ctx, p) })
			stream.Close()
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if e != io.EOF && e != nil && !errors.Is(e, errEventProcess) {
			var api *apiError
			if !errors.As(e, &api) || api.Code != "events_lost" {
				return e
			}
		}

		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		if backoff < 30*time.Second {
			backoff *= 2
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
		}
	}
}
