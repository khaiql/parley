package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/khaiql/parley/internal/adapter"
	"github.com/khaiql/parley/internal/paths"
	parleyRuntime "github.com/khaiql/parley/internal/runtime"
)

const (
	daemonStartupTimeout = 5 * time.Second
	daemonPollInterval   = 25 * time.Millisecond
)

func startRoomDaemonProcess(cfg roomDaemonConfig) (int, error) {
	exe, err := os.Executable()
	if err != nil {
		return 0, err
	}
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return 0, err
	}
	defer devNull.Close()

	cmd := exec.Command(
		exe,
		"__room-daemon",
		"--room", cfg.RoomID,
		"--topic", cfg.Topic,
		"--name", cfg.Name,
		"--role", cfg.Role,
	)
	cmd.Env = os.Environ()
	cmd.Stdin = nil
	cmd.Stdout = devNull
	cmd.Stderr = devNull
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	if err := waitForRoomDaemonReady(paths.New(paths.DefaultRoot()), cfg, cmd.Process); err != nil {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		return pid, err
	}
	if err := cmd.Process.Release(); err != nil {
		return pid, err
	}
	return pid, nil
}

func startParticipantDaemonProcess(cfg participantDaemonConfig) (int, error) {
	exe, err := os.Executable()
	if err != nil {
		return 0, err
	}
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return 0, err
	}
	defer devNull.Close()

	args := []string{
		"__participant-daemon",
		"--descriptor", cfg.Descriptor.String(),
		"--name", cfg.Name,
		"--role", cfg.Role,
	}
	if cfg.Directory != "" {
		args = append(args, "--dir", cfg.Directory)
	}
	if cfg.Repo != "" {
		args = append(args, "--repo", cfg.Repo)
	}

	cmd := exec.Command(exe, args...)
	cmd.Env = os.Environ()
	cmd.Stdin = nil
	cmd.Stdout = devNull
	cmd.Stderr = devNull
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	if err := waitForParticipantDaemonReady(paths.New(paths.DefaultRoot()), cfg, cmd.Process); err != nil {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		return pid, err
	}
	if err := cmd.Process.Release(); err != nil {
		return pid, err
	}
	return pid, nil
}

func waitForRoomDaemonReady(p paths.Paths, cfg roomDaemonConfig, proc *os.Process) error {
	deadline := time.Now().Add(daemonStartupTimeout)
	for time.Now().Before(deadline) {
		if err := processAlive(proc); err != nil {
			return fmt.Errorf("room daemon exited before becoming ready: %w", err)
		}
		meta, err := parleyRuntime.LoadRoomRuntime(p, cfg.RoomID)
		if err == nil && meta.LocalPort > 0 && controlReady(parleyRuntime.ServerSocketPath(p, cfg.RoomID)) && controlReady(parleyRuntime.ParticipantSocketPath(p, cfg.RoomID, cfg.Name)) {
			return nil
		}
		time.Sleep(daemonPollInterval)
	}
	return fmt.Errorf("room daemon did not become ready within %s", daemonStartupTimeout)
}

func waitForParticipantDaemonReady(p paths.Paths, cfg participantDaemonConfig, proc *os.Process) error {
	deadline := time.Now().Add(daemonStartupTimeout)
	for time.Now().Before(deadline) {
		if err := processAlive(proc); err != nil {
			return fmt.Errorf("participant daemon exited before becoming ready: %w", err)
		}
		store, err := parleyRuntime.ParticipantStore(p, cfg.Descriptor.RoomID, cfg.Name)
		if err != nil {
			return err
		}
		meta, err := store.LoadMeta()
		if err == nil && meta.Status == "online" && controlReady(parleyRuntime.ParticipantSocketPath(p, cfg.Descriptor.RoomID, cfg.Name)) {
			return nil
		}
		time.Sleep(daemonPollInterval)
	}
	return fmt.Errorf("participant daemon did not become ready within %s", daemonStartupTimeout)
}

func processAlive(proc *os.Process) error {
	if proc == nil {
		return nil
	}
	return proc.Signal(syscall.Signal(0))
}

func controlReady(socketPath string) bool {
	resp, err := adapter.CallControl(socketPath, adapter.ControlRequest{Type: "status"})
	return err == nil && resp.OK
}
