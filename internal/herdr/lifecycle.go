package herdr

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

func NewID() (string, error) {
	b := make([]byte, 16)
	_, err := rand.Read(b)
	return hex.EncodeToString(b), err
}

// NativeHost selects an injected socket through verified inventory, never guessed paths.
func NativeHost(ctx context.Context, c *Client) (HostSession, string, error) {
	b, err := c.runner()(ctx, c.HerdrBinary, []string{"session", "list", "--json"}, nil)
	if err != nil {
		return HostSession{}, "", err
	}
	var list struct {
		Sessions []struct {
			Name    string `json:"name"`
			Running bool   `json:"running"`
			Socket  string `json:"socket_path"`
		} `json:"sessions"`
	}
	if err = json.Unmarshal(b, &list); err != nil {
		return HostSession{}, "", err
	}
	injected := os.Getenv("HERDR_SOCKET_PATH")
	if injected != "" {
		injected, err = filepath.EvalSymlinks(injected)
		if err != nil {
			return HostSession{}, "", err
		}
	}
	session := os.Getenv("HERDR_SESSION")
	if session == "" {
		session = "default"
	}
	selected := ""
	for _, s := range list.Sessions {
		if !s.Running {
			continue
		}
		socket, e := filepath.EvalSymlinks(s.Socket)
		if e != nil {
			continue
		}
		if injected != "" && socket == injected || injected == "" && s.Name == session {
			if selected != "" {
				return HostSession{}, "", fmt.Errorf("ambiguous session")
			}
			selected = s.Name
		}
	}
	if selected == "" {
		return HostSession{}, "", fmt.Errorf("running native session required")
	}
	h, err := ResolveHost(ctx, c, selected)
	return h, selected, err
}
func validOwner(c SessionConfig, h HostSession, role string) error {
	if c.Role != role || c.SessionKey != h.Config.SessionKey || c.HerdrSocket != h.Config.HerdrSocket || safeID(c.ControllerID) != nil || safeID(c.MachineID) != nil || c.MachineID == "self" {
		return fmt.Errorf("corrupt controller ownership")
	}
	return nil
}
func (h HostSession) CoordinatorConfig() (SessionConfig, error) {
	var c SessionConfig
	err := readJSON(filepath.Join(h.ConfigDir, "coordinator.json"), &c)
	if err == nil {
		err = validOwner(c, h, "controller")
	}
	return c, err
}
func (h HostSession) Owner(ctx context.Context) (SessionConfig, error) {
	c, err := h.CoordinatorConfig()
	if err == nil {
		return c, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return c, err
	}
	err = h.controller(ctx, func(owner *SessionConfig) error { c = *owner; return nil })
	if err != nil {
		return c, err
	}
	if c.ControllerID == "" {
		c = h.Config
		c.MachineID = "local"
	}
	return c, nil
}
func (h HostSession) Setup(ctx context.Context, c *Client) (SessionConfig, error) {
	var owner SessionConfig
	if err := privateDir(h.ConfigDir); err != nil {
		return owner, err
	}
	err := lockFile(ctx, filepath.Join(h.ConfigDir, "controller.lock"), func() error {
		var participant SessionConfig
		err := readJSON(filepath.Join(h.ConfigDir, "controller.json"), &participant)
		if err == nil {
			return fmt.Errorf("controller_conflict")
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		owner, err = h.CoordinatorConfig()
		if err == nil {
			return nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		id, err := NewID()
		if err != nil {
			return err
		}
		owner = h.Config
		owner.Role = "controller"
		owner.ControllerID = id
		owner.MachineID = "local"
		return writeJSON(filepath.Join(h.ConfigDir, "coordinator.json"), owner)
	})
	if err != nil {
		return owner, err
	}
	if err = h.PinTrustedRuntime(c); err != nil {
		return owner, err
	}
	err = h.Store.Update(func(state *State) error {
		if state.ControllerID != "" && state.ControllerID != owner.ControllerID {
			return fmt.Errorf("controller_conflict")
		}
		state.ControllerID = owner.ControllerID
		return nil
	})
	return owner, err
}
func (h HostSession) CoordinatorSocket() (string, error) {
	key, err := SessionKey(h.Config.HerdrSocket)
	if err != nil {
		return "", err
	}
	// Full namespace hash fits Unix socket limits under the fixed short /tmp root.
	dir := filepath.Join("/tmp", "parley-h-"+strconv.Itoa(os.Getuid())+"-"+key)
	if err = privateDir(dir); err != nil {
		return "", err
	}
	return filepath.Join(dir, "broker"), nil
}

// LaunchHelper detaches only the new child and waits for its private ready socket.
func LaunchHelper(ctx context.Context, binary string, args []string, readySocket string) error {
	if err := privateSocketDir(readySocket); err != nil {
		return err
	}
	if conn, err := net.DialTimeout("unix", readySocket, 100*time.Millisecond); err == nil {
		conn.Close()
		return nil
	}
	log, err := privateFile(filepath.Join(filepath.Dir(readySocket), "broker.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND)
	if err != nil {
		return err
	}
	defer log.Close()
	// #nosec G204 -- Caller supplies the trusted executable and fixed lifecycle argv.
	cmd := exec.Command(binary, args...)
	cmd.Env = os.Environ()
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err = cmd.Start(); err != nil {
		return err
	}
	ready := false
	defer func() {
		if !ready {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
	}()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("coordinator readiness timeout; inspect %s", log.Name())
		case <-tick.C:
			conn, err := net.DialTimeout("unix", readySocket, 100*time.Millisecond)
			if err == nil {
				conn.Close()
				if err = cmd.Process.Release(); err != nil {
					return err
				}
				ready = true
				return nil
			}
		}
	}
}
func (h HostSession) Startup(ctx context.Context, binary, session string) error {
	_, err := h.CoordinatorConfig()
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err = validateSession(session); err != nil {
		return err
	}
	path, err := h.CoordinatorSocket()
	if err != nil {
		return err
	}
	return lockFile(ctx, path+".launch", func() error {
		if err := LaunchHelper(ctx, binary, []string{"herdr", "broker", "--native-session", session}, path); err != nil {
			return err
		}
		return h.CheckCoordinator(ctx)
	})
}

func (c *Client) VerifyVersion(ctx context.Context) error {
	b, err := c.runner()(ctx, c.HerdrBinary, []string{"--version"}, nil)
	if err != nil {
		return err
	}
	version := strings.TrimSpace(strings.TrimPrefix(string(b), "herdr "))
	var major, minor, patch int
	if _, err = fmt.Sscanf(version, "%d.%d.%d", &major, &minor, &patch); err != nil {
		return fmt.Errorf("Herdr 0.9.3 or newer required")
	}
	if major == 0 && (minor < 9 || minor == 9 && patch < 3) {
		return fmt.Errorf("Herdr 0.9.3 or newer required")
	}
	return nil
}
func (c *Client) pluginEnabled(ctx context.Context) (bool, error) {
	b, err := c.runner()(ctx, c.HerdrBinary, []string{"plugin", "list", "--json"}, nil)
	if err != nil {
		return false, err
	}
	_, err = installedHelper(b)
	if err != nil && (err.Error() == "helper_disabled" || err.Error() == "helper_missing") {
		return false, nil
	}
	return err == nil, err
}

type subscription struct {
	peer   Peer
	cancel context.CancelFunc
}

// Coordinate owns its streams and masters for the whole detached process lifetime.
func (h HostSession) Coordinate(ctx context.Context, c *Client) error {
	owner, err := h.CoordinatorConfig()
	if err != nil {
		return err
	}
	c.LocalMachineID = owner.MachineID
	c.HerdrSocket = owner.HerdrSocket
	manager := &TunnelManager{}
	defer manager.Close()
	broker := &Broker{Store: h.Store, Client: c, Tunnels: manager}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var workers sync.WaitGroup
	defer workers.Wait()
	subscriptions := map[string]subscription{}
	defer func() {
		for _, s := range subscriptions {
			s.cancel()
		}
		cancel()
	}()
	// Local disable must cancel owned work even while a remote refresh is blocked.
	workers.Go(func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				enabled, err := c.pluginEnabled(ctx)
				if err != nil {
					if ctx.Err() == nil {
						fmt.Fprintln(os.Stderr, err)
					}
					continue
				}
				if !enabled {
					cancel()
					return
				}
			}
		}
	})
	local := Peer{Machine: Machine{ID: owner.MachineID}, Config: owner}
	workers.Go(func() {
		if err := watchLocal(ctx, c.HerdrSocket, func(ctx context.Context) error {
			if err := broker.Drain(ctx, local); err != nil {
				return err
			}
			return (&HostOperations{Store: h.Store, Client: c}).ReconcileTerminal(ctx)
		}); err != nil && ctx.Err() == nil {
			fmt.Fprintln(os.Stderr, err)
		}
	})
	path, err := h.CoordinatorSocket()
	if err != nil {
		return err
	}
	failed := make(chan error, 1)
	workers.Go(func() {
		failed <- ServeLocal(ctx, path, local, func(ctx context.Context, p Peer, r Request) (Response, error) {
			if r.Op == "status" || r.Op == "probe" || r.Op == "catalog" {
				return broker.Handle(ctx, p, r)
			}
			if r.Op != "join" {
				return failure("invalid_request", nil), nil
			}
			return broker.Handle(ctx, p, r)
		})
	})
	deactivate := func(peer Peer) {
		manager.ClosePeer(peer)
		c.managed.Delete(peer.Machine.ID)
		if err := h.Store.Update(func(state *State) error {
			saved, ok := state.Peers[peer.Machine.ID]
			if ok && saved.Config.SessionKey == peer.Config.SessionKey {
				saved.Machine.Enabled = false
				state.Peers[peer.Machine.ID] = saved
			}
			return nil
		}); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
	}
	refresh := func() {
		machines, e := c.Machines(ctx)
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			return
		}
		wanted := map[string]bool{}
		for _, m := range machines {
			if !m.Enabled {
				continue
			}
			wanted[m.ID] = true
			p, e := ManagePeer(ctx, c, h.Store, m)
			if e != nil {
				fmt.Fprintln(os.Stderr, "peer", m.ID, e)
				continue
			}
			prior, ok := subscriptions[m.ID]
			if ok && prior.peer == p {
				continue
			}
			if ok {
				prior.cancel()
			}
			streamCtx, stop := context.WithCancel(ctx)
			subscriptions[m.ID] = subscription{p, stop}
			workers.Go(func() {
				e := c.WatchRequests(streamCtx, p, func(ctx context.Context, peer Peer) error {
					if err := broker.Drain(ctx, peer); err != nil {
						return err
					}
					return broker.reconcileTerminalPeer(ctx, peer)
				})
				if e != nil && streamCtx.Err() == nil {
					fmt.Fprintln(os.Stderr, "peer", p.Machine.ID, e)
				}
			})
		}
		for id, s := range subscriptions {
			if !wanted[id] {
				deactivate(s.peer)
				s.cancel()
				delete(subscriptions, id)
			}
		}
	}
	refresh()
	profiles := time.NewTicker(30 * time.Second)
	defer profiles.Stop()
	status := time.NewTicker(2 * time.Second)
	defer status.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-failed:
			return err
		case <-profiles.C:
			refresh()
		case <-status.C:
			for id, s := range subscriptions {
				if _, check := c.helper(ctx, s.peer.Machine); check != nil && (check.Error() == "helper_disabled" || check.Error() == "helper_missing") {
					s.cancel()
					deactivate(s.peer)
					delete(subscriptions, id)
				}
			}
			if e := broker.ReconcileTerminals(ctx); e != nil {
				fmt.Fprintln(os.Stderr, e)
			}
			if e := broker.Reconcile(ctx); e != nil {
				fmt.Fprintln(os.Stderr, e)
			}
		}
	}
}
func watchLocal(ctx context.Context, socket string, drain func(context.Context) error) error {
	backoff := time.Second
	for {
		attempt, stop := context.WithCancel(ctx)
		reader, writer := net.Pipe()
		done := make(chan error, 1)
		go func() { err := RelayEvents(attempt, socket, writer); writer.Close(); done <- err }()
		err := ReadEvents(ctx, reader, func() error { backoff = time.Second; return drain(ctx) }, func() error { return drain(ctx) })
		reader.Close()
		stop()
		relayErr := <-done
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !isEventTransportError(err) {
			return err
		}
		if relayErr != nil && !isEventTransportError(relayErr) {
			return relayErr
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

// PinTrustedRuntime reads the existing immutable pin before consulting native environment.
func (h HostSession) PinTrustedRuntime(c *Client) error {
	if _, err := h.Store.pinnedPaths(); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	p, err := c.runtimePaths()
	if err != nil {
		return err
	}
	return h.Store.PinRuntimeRoot(p.Root)
}

// Do not replace a running older coordinator and silently drop its live routes.
func (h HostSession) CheckCoordinator(ctx context.Context) error {
	owner, err := h.CoordinatorConfig()
	if err != nil {
		return err
	}
	path, err := h.CoordinatorSocket()
	if err != nil {
		return err
	}
	result, err := CallLocal(ctx, path, Request{ID: "version", Op: "probe", Target: AgentTarget{MachineID: owner.MachineID, HerdrSocket: owner.HerdrSocket}})
	if err != nil {
		return fmt.Errorf("coordinator_unavailable: %w", err)
	}
	if result.Version != HelperVersion {
		return fmt.Errorf("coordinator_upgrade_required: rebuild the plugin, disable/enable only Parley, then run parley herdr setup; existing SSH routes close during this plugin restart, agents remain running")
	}
	return nil
}
