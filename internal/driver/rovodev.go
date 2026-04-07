package driver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// RovodevDriver drives a Rovodev agent via `acli rovodev serve` (HTTP/SSE).
//
// Each driver instance owns one `serve` child process on a unique localhost
// port, ensuring full session isolation between Parley agents.
type RovodevDriver struct {
	mu        sync.Mutex
	cmd       *exec.Cmd
	cancel    context.CancelFunc
	events    chan AgentEvent
	port      int
	client    *http.Client
	wg        sync.WaitGroup
	closeOnce sync.Once
}

// Start spawns `acli rovodev serve --disable-session-token <port>`, waits for
// the healthcheck endpoint to become healthy, and sends the initial message.
func (d *RovodevDriver) Start(ctx context.Context, config AgentConfig) error {
	ctx, cancel := context.WithCancel(ctx)
	d.cancel = cancel

	port, err := freePort()
	if err != nil {
		cancel()
		return fmt.Errorf("rovodev: find free port: %w", err)
	}
	d.port = port
	d.client = &http.Client{Timeout: 0}
	d.events = make(chan AgentEvent, 64)

	args := []string{"rovodev", "serve", "--disable-session-token", fmt.Sprintf("%d", port)}
	args = append(args, config.Args...)

	command := config.Command
	if command == "" {
		command = "acli"
	}

	d.cmd = exec.CommandContext(ctx, command, args...)
	if err := d.cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("rovodev: start process: %w", err)
	}

	if err := d.waitHealthy(ctx); err != nil {
		_ = d.Stop()
		return err
	}

	if config.InitialMessage != "" {
		if err := d.Send(config.InitialMessage); err != nil {
			_ = d.Stop()
			return fmt.Errorf("rovodev: send initial message: %w", err)
		}
	}

	return nil
}

// Send posts a chat message to the V3 API and streams back SSE events,
// emitting AgentEvents on the events channel.
func (d *RovodevDriver) Send(text string) error {
	d.mu.Lock()
	port := d.port
	d.mu.Unlock()

	base := fmt.Sprintf("http://localhost:%d/v3", port)

	payload, _ := json.Marshal(map[string]interface{}{
		"message":          text,
		"enable_deep_plan": false,
	})
	resp, err := d.client.Post(base+"/set_chat_message", "application/json", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("rovodev: set_chat_message: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("rovodev: set_chat_message returned %d", resp.StatusCode)
	}

	req, err := http.NewRequest("GET", base+"/stream_chat", nil)
	if err != nil {
		return fmt.Errorf("rovodev: create stream request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")

	streamResp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("rovodev: stream_chat: %w", err)
	}
	if streamResp.StatusCode != http.StatusOK {
		streamResp.Body.Close()
		return fmt.Errorf("rovodev: stream_chat returned %d", streamResp.StatusCode)
	}

	d.wg.Add(1)
	go d.readSSE(streamResp.Body)

	return nil
}

// Events returns the channel on which AgentEvents are delivered.
func (d *RovodevDriver) Events() <-chan AgentEvent {
	return d.events
}

// Stop terminates the serve child process and waits for goroutines to finish.
func (d *RovodevDriver) Stop() error {
	if d.cancel != nil {
		d.cancel()
	}
	if d.cmd != nil && d.cmd.Process != nil {
		_ = d.cmd.Process.Kill()
		_ = d.cmd.Wait()
	}
	d.wg.Wait()
	d.closeEvents()
	return nil
}

// closeEvents closes the events channel exactly once, safe for concurrent use.
func (d *RovodevDriver) closeEvents() {
	d.closeOnce.Do(func() {
		if d.events != nil {
			close(d.events)
		}
	})
}

// SessionID returns the session ID if available. For the first pass, this
// is empty since we rely on process-scoped sessions.
func (d *RovodevDriver) SessionID() string {
	return ""
}

// waitHealthy polls the /healthcheck endpoint until it returns healthy or the
// context is cancelled.
func (d *RovodevDriver) waitHealthy(ctx context.Context) error {
	healthURL := fmt.Sprintf("http://localhost:%d/healthcheck", d.port)
	deadline := time.After(60 * time.Second)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("rovodev: context cancelled waiting for healthcheck")
		case <-deadline:
			return fmt.Errorf("rovodev: healthcheck timeout after 60s on port %d", d.port)
		case <-ticker.C:
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
			if err != nil {
				continue
			}
			resp, err := d.client.Do(req)
			if err != nil {
				continue
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK && strings.Contains(string(body), "healthy") {
				return nil
			}
		}
	}
}

// readSSE reads the SSE stream from a stream_chat response body and emits
// AgentEvents. The goroutine finishes when the stream ends or the close event
// is received.
func (d *RovodevDriver) readSSE(body io.ReadCloser) {
	defer d.wg.Done()
	defer body.Close()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 256*1024), 256*1024)

	var eventType string
	var dataLines []string

	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "event: ") {
			eventType = strings.TrimPrefix(line, "event: ")
			continue
		}
		if strings.HasPrefix(line, "data: ") {
			dataLines = append(dataLines, strings.TrimPrefix(line, "data: "))
			continue
		}
		if line == "" && eventType != "" {
			data := strings.Join(dataLines, "\n")
			d.handleSSEEvent(eventType, data)
			if eventType == "close" {
				return
			}
			eventType = ""
			dataLines = dataLines[:0]
		}
	}
}

// handleSSEEvent maps a single Rovodev SSE event to an AgentEvent.
func (d *RovodevDriver) handleSSEEvent(eventType, data string) {
	switch eventType {
	case "part_start":
		d.handlePartStart(data)
	case "part_delta":
		d.handlePartDelta(data)
	case "on_call_tools_start":
		d.handleToolStart(data)
	case "exception":
		d.events <- AgentEvent{Type: EventError, Text: data}
	case "close":
		d.events <- AgentEvent{Type: EventDone}
	}
}

// rovodevPart is the shape of a part_start payload.
type rovodevPart struct {
	Part struct {
		PartKind string `json:"part_kind"`
		Content  string `json:"content"`
		ToolName string `json:"tool_name"`
	} `json:"part"`
}

// rovodevDelta is the shape of a part_delta payload.
type rovodevDelta struct {
	Delta struct {
		PartDeltaKind string `json:"part_delta_kind"`
		ContentDelta  string `json:"content_delta"`
	} `json:"delta"`
}

// rovodevToolCall is the shape of an on_call_tools_start payload.
type rovodevToolCall struct {
	ToolCalls []struct {
		ToolName string `json:"tool_name"`
	} `json:"tool_calls"`
}

func (d *RovodevDriver) handlePartStart(data string) {
	var p rovodevPart
	if err := json.Unmarshal([]byte(data), &p); err != nil {
		return
	}
	switch p.Part.PartKind {
	case "thinking":
		d.events <- AgentEvent{Type: EventThinking}
	case "text":
		if p.Part.Content != "" {
			d.events <- AgentEvent{Type: EventText, Text: p.Part.Content}
		}
	case "tool-call":
		d.events <- AgentEvent{Type: EventToolUse, ToolName: p.Part.ToolName}
	}
}

func (d *RovodevDriver) handlePartDelta(data string) {
	var delta rovodevDelta
	if err := json.Unmarshal([]byte(data), &delta); err != nil {
		return
	}
	if delta.Delta.PartDeltaKind == "text" && delta.Delta.ContentDelta != "" {
		d.events <- AgentEvent{Type: EventText, Text: delta.Delta.ContentDelta}
	}
}

func (d *RovodevDriver) handleToolStart(data string) {
	var tc rovodevToolCall
	if err := json.Unmarshal([]byte(data), &tc); err != nil {
		return
	}
	for _, call := range tc.ToolCalls {
		d.events <- AgentEvent{Type: EventToolUse, ToolName: call.ToolName}
	}
}

// freePort asks the OS for an available TCP port.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		return 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port, nil
}
