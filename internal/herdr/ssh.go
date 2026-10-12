package herdr

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

type Master struct {
	Command     *exec.Cmd
	ControlPath string
	Target      string
	cancel      context.CancelFunc
	done        chan struct{}
	err         error
	once        sync.Once
	dir         string
}

func StartMaster(ctx context.Context, p Peer) (*Master, error) {
	if e := validateMachine(p.Machine); e != nil {
		return nil, e
	}
	dir, e := os.MkdirTemp("/tmp", "parley-ssh-")
	if e != nil {
		return nil, e
	}
	if e = os.Chmod(dir, 0700); e != nil {
		os.RemoveAll(dir)
		return nil, e
	}
	ctx, cancel := context.WithCancel(ctx)
	m := &Master{ControlPath: filepath.Join(dir, "master"), Target: p.Machine.Target, cancel: cancel, done: make(chan struct{}), dir: dir}
	m.Command = exec.CommandContext(ctx, "ssh", "-M", "-S", m.ControlPath, "-N", "-o", "BatchMode=yes", "-o", "ExitOnForwardFailure=yes", "-o", "ControlPersist=no", "-o", "ClearAllForwardings=yes", "-o", "ServerAliveInterval=30", "-o", "ServerAliveCountMax=3", "--", m.Target)
	m.Command.Stderr = &boundedBuffer{limit: 64 * 1024}
	m.Command.WaitDelay = processWaitDelay
	if e = m.Command.Start(); e != nil {
		cancel()
		os.RemoveAll(dir)
		return nil, e
	}
	go func() { m.err = m.Command.Wait(); close(m.done) }()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			m.Close()
			return nil, ctx.Err()
		case <-m.done:
			m.Close()
			return nil, fmt.Errorf("SSH master exited: %w", m.err)
		case <-deadline.C:
			m.Close()
			return nil, fmt.Errorf("SSH master readiness timeout")
		case <-tick.C:
			check, stop := context.WithTimeout(ctx, time.Second)
			_, e := RunProcess(check, "ssh", []string{"-S", m.ControlPath, "-O", "check", "-o", "BatchMode=yes", "--", m.Target}, nil)
			stop()
			if e == nil {
				return m, nil
			}
		}
	}
}
func (m *Master) Close() error {
	m.once.Do(func() { m.cancel(); <-m.done; _ = os.RemoveAll(m.dir) })
	return m.err
}
