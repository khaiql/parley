package herdr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sync"
)

type Run func(context.Context, string, []string, []byte) ([]byte, error)

const outputLimit = 8 * 1024 * 1024

// A failing writer stops the child instead of retaining an unbounded response.
type boundedBuffer struct {
	buffer   bytes.Buffer
	limit    int
	overflow bool
	mu       sync.Mutex
	cancel   context.CancelFunc
}

func (b *boundedBuffer) Len() int       { return b.buffer.Len() }
func (b *boundedBuffer) Bytes() []byte  { return b.buffer.Bytes() }
func (b *boundedBuffer) String() string { return b.buffer.String() }
func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(p) > b.limit-b.Len() {
		b.overflow = true
		if b.cancel != nil {
			b.cancel()
		}
		return 0, fmt.Errorf("process output exceeds limit")
	}
	return b.buffer.Write(p)
}
func RunProcess(ctx context.Context, binary string, args []string, stdin []byte) ([]byte, error) {
	return runProcessEnv(ctx, binary, args, stdin, nil)
}
func runProcessEnv(ctx context.Context, binary string, args []string, stdin []byte, env []string) ([]byte, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	if env != nil {
		cmd.Env = env
	}
	cmd.Stdin = bytes.NewReader(stdin)
	out := &boundedBuffer{limit: outputLimit, cancel: cancel}
	stderr := &boundedBuffer{limit: 64 * 1024, cancel: cancel}
	cmd.Stdout = out
	cmd.Stderr = stderr
	// exec pipes need a kill when a writer rejects output.
	cmd.WaitDelay = processWaitDelay
	err := cmd.Run()
	if out.overflow || stderr.overflow {
		return nil, fmt.Errorf("process output exceeds limit")
	}
	if err != nil {
		// CLI helpers return structured failures on stdout, even when SSH succeeds.
		var reply struct {
			Error *apiError `json:"error"`
		}
		if json.Unmarshal(out.Bytes(), &reply) == nil && reply.Error != nil && reply.Error.Message != "" {
			return nil, fmt.Errorf("host process: %w: %s: %s", err, reply.Error, stderr.String())
		}
		return nil, fmt.Errorf("host process: %w: %s", err, stderr.String())
	}
	return out.Bytes(), nil
}
