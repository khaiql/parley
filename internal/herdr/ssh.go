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
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/khaiql/parley/internal/descriptor"
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
	// #nosec G204 -- The saved target is validated and passed as argv after --; the control path is privately allocated.
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
			_, e := RunProcess(check, "ssh", muxArgs(m, "check", nil), nil)
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

// RouteKind reflects routes whose SSH connection originates on the controller.
func RouteKind(hostMachineID, participantMachineID string) (string, error) {
	if safeID(hostMachineID) != nil || safeID(participantMachineID) != nil || hostMachineID == "self" || participantMachineID == "self" {
		return "", fmt.Errorf("normalized machine required")
	}
	switch {
	case hostMachineID == participantMachineID:
		return "direct", nil
	case hostMachineID == "local":
		return "reverse", nil
	case participantMachineID == "local":
		return "local", nil
	default:
		return "", fmt.Errorf("unsupported_route")
	}
}

type tunnelForward struct {
	kind, spec   string
	cancelFailed bool
}
type tunnelLease struct {
	master      *Master
	endpoints   Endpoints
	forwards    []tunnelForward
	bindings    map[string]bool
	unavailable bool
}
type TunnelManager struct {
	Run     Run
	Masters map[string]*Master
	mu      sync.Mutex
	leases  map[string]*tunnelLease
	closed  bool
}

func (m *TunnelManager) runner() Run {
	if m.Run != nil {
		return m.Run
	}
	return RunProcess
}
func muxArgs(master *Master, operation string, forward *tunnelForward) []string {
	args := []string{"-F", "/dev/null", "-S", master.ControlPath, "-O", operation, "-o", "BatchMode=yes"}
	if forward != nil {
		args = append(args, forward.kind, forward.spec)
	}
	return append(args, "--", master.Target)
}
func (m *TunnelManager) mux(ctx context.Context, master *Master, operation string, forward *tunnelForward) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	b, err := m.runner()(ctx, "ssh", muxArgs(master, operation, forward), nil)
	if ctx.Err() != nil {
		return b, ctx.Err()
	}
	return b, err
}
func masterAlive(master *Master) bool {
	select {
	case <-master.done:
		return false
	default:
		return true
	}
}

// AcquisitionError marks forwarding ownership that a failed acquisition could not clear.
// All other Acquire failures occur before allocation or after confirmed cleanup.
type AcquisitionError struct{ Cause error }

func (e *AcquisitionError) Error() string { return e.Cause.Error() }
func (e *AcquisitionError) Unwrap() error { return e.Cause }
func (m *TunnelManager) Acquire(ctx context.Context, peer Peer, room Room, target AgentTarget) (Endpoints, error) {
	var empty Endpoints
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	kind, err := RouteKind(room.Host.MachineID, target.MachineID)
	if err != nil {
		return empty, err
	}
	if room.Closed {
		return empty, fmt.Errorf("route_unavailable")
	}
	if err := validateRequest(Request{ID: "route", Op: "join", RoomID: room.ID, Target: target}); err != nil {
		return empty, err
	}
	d, u, err := validateEndpoints(room.Source, room.ID)
	if err != nil {
		return empty, err
	}
	if _, err := SessionKey(room.Host.HerdrSocket); err != nil {
		return empty, err
	}
	remote := target
	if kind == "local" {
		remote = room.Host
	}
	if kind != "direct" || target.MachineID != "local" {
		key, err := SessionKey(remote.HerdrSocket)
		if err != nil || remote.MachineID != peer.Machine.ID || remote.MachineID != peer.Config.MachineID || remote.HerdrSocket != peer.Config.HerdrSocket || key != peer.Config.SessionKey {
			return empty, fmt.Errorf("session_mismatch")
		}
		if err := validateMachine(peer.Machine); err != nil {
			return empty, err
		}
	}
	tuple, _ := json.Marshal([5]string{room.Host.MachineID, room.Host.HerdrSocket, target.MachineID, target.HerdrSocket, room.ID})
	routeKey := hashBytes(tuple)
	binding := target.MachineID + ":" + HostBindingKey(room.ID, target)
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	if m.closed {
		return empty, fmt.Errorf("route_unavailable")
	}
	if m.leases == nil {
		m.leases = map[string]*tunnelLease{}
	}
	if old := m.leases[routeKey]; old != nil {
		if old.unavailable || old.master != nil && !masterAlive(old.master) {
			old.unavailable = true
			if old.bindings[binding] {
				return empty, &AcquisitionError{Cause: fmt.Errorf("route_unavailable")}
			}
			return empty, fmt.Errorf("route_unavailable")
		}
		if old.bindings[binding] {
			return old.endpoints, nil
		}
		if err := m.probe(ctx, peer, old.endpoints, room.ID, target); err != nil {
			if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
				old.unavailable = true
			}
			return empty, fmt.Errorf("route_unavailable: %w", err)
		}
		old.bindings[binding] = true
		return old.endpoints, nil
	}
	lease := &tunnelLease{endpoints: room.Source, bindings: map[string]bool{binding: true}}
	if kind != "direct" {
		masterKey := remote.MachineID + ":" + peer.Config.SessionKey
		if m.Masters == nil {
			m.Masters = map[string]*Master{}
		}
		master := m.Masters[masterKey]
		if master == nil {
			// Manager owns master lifetime; a Join deadline must not kill other leases.
			master, err = StartMaster(context.Background(), peer)
			if err != nil {
				return empty, err
			}
			m.Masters[masterKey] = master
		}
		if !masterAlive(master) {
			return empty, fmt.Errorf("route_unavailable")
		}
		lease.master = master
		ports := []int{d.Port, 0}
		ports[1], _ = strconv.Atoi(u.Port())
		allocated := make([]int, 2)
		for i, source := range ports {
			allocated[i], err = m.forward(ctx, lease, kind, source)
			if err != nil {
				break
			}
		}
		if err == nil {
			lease.endpoints = Endpoints{Descriptor: descriptor.Descriptor{Host: "127.0.0.1", Port: allocated[0], RoomID: room.ID}.String(), ArtifactURL: "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(allocated[1])) + "/rooms/" + room.ID + "/artifacts"}
		}
	}
	if err == nil {
		err = m.probe(ctx, peer, lease.endpoints, room.ID, target)
	}
	if err != nil {
		cleanupErr := m.cancelForwards(context.Background(), lease, true)
		if cleanupErr != nil {
			lease.unavailable = true
			m.leases[routeKey] = lease
			return empty, &AcquisitionError{Cause: fmt.Errorf("route_unavailable: %w; retained forwards: %v", err, cleanupErr)}
		}
		return empty, err
	}
	m.leases[routeKey] = lease
	return lease.endpoints, nil
}
func (m *TunnelManager) probe(ctx context.Context, peer Peer, endpoints Endpoints, roomID string, target AgentTarget) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if target.MachineID == "local" {
		return probeRoute(ctx, endpoints, roomID)
	}
	c := Client{Run: m.runner()}
	b, err := c.Call(ctx, peer, RPCRequest{Operation: "probe-route", ControllerID: peer.Config.ControllerID, SessionKey: peer.Config.SessionKey, RoomID: roomID, Endpoints: &endpoints})
	if err != nil {
		return err
	}
	var result struct {
		Status string `json:"status"`
	}
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&result) != nil || decoder.Decode(new(any)) != io.EOF || result.Status != "ready" {
		return fmt.Errorf("route probe rejected")
	}
	return nil
}
func (m *TunnelManager) forward(ctx context.Context, lease *tunnelLease, kind string, source int) (int, error) {
	flag := "-R"
	attempts := 1
	if kind == "local" {
		flag = "-L"
		attempts = 3
	}
	var allocationErr error
	for i := 0; i < attempts; i++ {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		port := 0
		if flag == "-L" {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				return 0, err
			}
			port = listener.Addr().(*net.TCPAddr).Port
			_ = listener.Close()
		}
		forward := tunnelForward{kind: flag, spec: fmt.Sprintf("127.0.0.1:%d:127.0.0.1:%d", port, source)}
		// A lost mux response does not prove that allocation had no effect.
		index := len(lease.forwards)
		lease.forwards = append(lease.forwards, forward)
		output, err := m.mux(ctx, lease.master, "forward", &forward)
		if err != nil {
			allocationErr = err
			if flag == "-R" {
				return 0, err
			}
			// Retry only after this exact candidate is conclusively canceled.
			if _, cleanupErr := m.mux(context.Background(), lease.master, "cancel", &forward); cleanupErr != nil {
				lease.forwards[index].cancelFailed = true
				return 0, errors.Join(err, cleanupErr)
			}
			lease.forwards = lease.forwards[:index]
			continue
		}
		if flag == "-R" {
			text := strings.TrimSuffix(string(output), "\n")
			port, err = strconv.Atoi(text)
			if err != nil || port < 1 || port > 65535 || strconv.Itoa(port) != text {
				return 0, fmt.Errorf("invalid remote allocation")
			}
			lease.forwards[index].spec = fmt.Sprintf("127.0.0.1:%d:127.0.0.1:%d", port, source)
		}
		return port, nil
	}
	return 0, allocationErr
}
func (m *TunnelManager) cancelForwards(ctx context.Context, lease *tunnelLease, rollback bool) error {
	var retained []tunnelForward
	var errs []error
	for _, forward := range lease.forwards {
		if rollback && forward.cancelFailed {
			retained = append(retained, forward)
			errs = append(errs, fmt.Errorf("candidate cancellation remains uncertain"))
			continue
		}
		if _, err := m.mux(ctx, lease.master, "cancel", &forward); err != nil {
			retained = append(retained, forward)
			errs = append(errs, err)
		}
	}
	lease.forwards = retained
	return errors.Join(errs...)
}
func (m *TunnelManager) Release(ctx context.Context, roomID string, target AgentTarget) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	binding := target.MachineID + ":" + HostBindingKey(roomID, target)
	for key, lease := range m.leases {
		if !lease.bindings[binding] {
			continue
		}
		if len(lease.bindings) > 1 {
			delete(lease.bindings, binding)
			continue
		}
		if err := m.cancelForwards(ctx, lease, false); err != nil {
			lease.unavailable = true
			return fmt.Errorf("route_unavailable: retained forwards: %w", err)
		}
		delete(m.leases, key)
	}
	return nil
}

// Status includes failed cleanup so retained resources cannot look like usable routes.
func (m *TunnelManager) Status() map[string]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]string{}
	for key, lease := range m.leases {
		status := "ready"
		if lease.unavailable || lease.master != nil && !masterAlive(lease.master) {
			status = "route_unavailable"
		}
		out[key] = status
	}
	return out
}
func (m *TunnelManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	for _, master := range m.Masters {
		_ = master.Close()
	}
	m.leases = nil
	m.Masters = nil
	return nil
}

// ClosePeer closes only this manager's children for the exact activated session.
// Existing leases stay unavailable; enabling a plugin cannot recreate participants.
func (m *TunnelManager) ClosePeer(peer Peer) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := peer.Machine.ID + ":" + peer.Config.SessionKey
	master := m.Masters[key]
	if master == nil {
		return
	}
	for _, lease := range m.leases {
		if lease.master == master {
			lease.unavailable = true
		}
	}
	_ = master.Close()
	delete(m.Masters, key)
}
