package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestQuietStartupMaster(t *testing.T) {
	dir := t.TempDir()
	if e := os.WriteFile(filepath.Join(dir, "ssh"), []byte("#!/bin/sh\ncase \"$*\" in *'-O check'*) exit 0;; esac\nexec sleep 60\n"), 0700); e != nil {
		t.Fatal(e)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	m, e := StartMaster(context.Background(), Peer{Machine: Machine{ID: "peer", Target: "host", Session: "agents"}})
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	args := strings.Join(m.Command.Args, " ")
	for _, flag := range []string{"-M", "-S", "-N", "ControlPersist=no", "ClearAllForwardings=yes"} {
		if !strings.Contains(args, flag) {
			t.Fatal(args)
		}
	}
	if strings.Contains(args, "-L") || strings.Contains(args, "-R") {
		t.Fatal(args)
	}
	m.Close()
	if m.Command.ProcessState == nil {
		t.Fatal("master not reaped")
	}
	if _, e = os.Stat(filepath.Dir(m.ControlPath)); !os.IsNotExist(e) {
		t.Fatal("control root survived", e)
	}
}

func TestRouteKind(t *testing.T) {
	for _, c := range []struct {
		host, participant, want string
		bad                     bool
	}{{"local", "local", "direct", false}, {"a", "a", "direct", false}, {"local", "a", "reverse", false}, {"a", "local", "local", false}, {"a", "b", "", true}} {
		got, e := RouteKind(c.host, c.participant)
		if got != c.want || (e != nil) != c.bad {
			t.Fatal(c, got, e)
		}
	}
}

func tunnelFixture(t *testing.T) (*TunnelManager, Peer, Room, AgentTarget, *[]string) {
	t.Helper()
	socket := "/tmp/tunnel-herdr.sock"
	key, _ := SessionKey(socket)
	p := Peer{Machine: Machine{ID: "peer", Target: "host", Session: "agents"}, Config: SessionConfig{MachineID: "peer", HerdrSocket: socket, SessionKey: key, ControllerID: "controller"}, Binary: "/opt/plugin/bin/parley"}
	target := AgentTarget{MachineID: "peer", HerdrSocket: socket, WorkspaceID: "w1", PaneID: "p1", TerminalID: "term1", Kind: "codex", NativeSession: "native1"}
	room := Room{ID: "r", Host: AgentTarget{MachineID: "local", HerdrSocket: "/tmp/local-herdr.sock"}, Source: Endpoints{Descriptor: "parley://127.0.0.1:40001/r", ArtifactURL: "http://127.0.0.1:40002/rooms/r/artifacts"}}
	calls := []string{}
	manager := &TunnelManager{Masters: map[string]*Master{"peer:" + key: {ControlPath: "/tmp/owned/master", Target: "host", done: make(chan struct{})}}}
	next := 51000
	manager.Run = func(ctx context.Context, b string, args []string, in []byte) ([]byte, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("unbounded command")
		}
		calls = append(calls, strings.Join(args, " "))
		if strings.Contains(strings.Join(args, " "), "-O forward") {
			next++
			return []byte(fmt.Sprintf("%d\n", next)), nil
		}
		if len(in) > 0 {
			var r RPCRequest
			if err := json.Unmarshal(in, &r); err != nil || r.Operation != "probe-route" {
				t.Fatal(string(in), err)
			}
			return []byte(`{"status":"ready"}`), nil
		}
		return nil, nil
	}
	return manager, p, room, target, &calls
}
func TestTunnelReverseSharedBindings(t *testing.T) {
	m, p, r, target, calls := tunnelFixture(t)
	endpoints, e := m.Acquire(context.Background(), p, r, target)
	if e != nil {
		t.Fatal(e)
	}
	if endpoints.Descriptor != "parley://127.0.0.1:51001/r" || endpoints.ArtifactURL != "http://127.0.0.1:51002/rooms/r/artifacts" {
		t.Fatal(endpoints)
	}
	if _, e = m.Acquire(context.Background(), p, r, target); e != nil {
		t.Fatal(e)
	}
	second := target
	second.TerminalID = "term2"
	second.NativeSession = "native2"
	if _, e = m.Acquire(context.Background(), p, r, second); e != nil {
		t.Fatal(e)
	}
	for _, call := range *calls {
		if strings.Contains(call, "-O forward") && (!strings.Contains(call, "-F /dev/null") || !strings.Contains(call, "-R 127.0.0.1:0:127.0.0.1:")) {
			t.Fatal(call)
		}
	}
	count := func(s string) int {
		n := 0
		for _, call := range *calls {
			if strings.Contains(call, s) {
				n++
			}
		}
		return n
	}
	if count("-O forward") != 2 {
		t.Fatal(*calls)
	}
	if e = m.Release(context.Background(), r.ID, target); e != nil {
		t.Fatal(e)
	}
	if count("-O cancel") != 0 {
		t.Fatal(*calls)
	}
	if e = m.Release(context.Background(), r.ID, second); e != nil {
		t.Fatal(e)
	}
	if count("-O cancel") != 2 || len(m.Status()) != 0 {
		t.Fatal(*calls, m.Status())
	}
	if !strings.Contains((*calls)[len(*calls)-2], "-R 127.0.0.1:51001:127.0.0.1:40001") {
		t.Fatal(*calls)
	}
}
func TestTunnelRollback(t *testing.T) {
	for _, failure := range []string{"second", "malformed", "probe", "cancel", "canceled"} {
		t.Run(failure, func(t *testing.T) {
			m, p, r, target, calls := tunnelFixture(t)
			run := m.Run
			forwards := 0
			m.Run = func(ctx context.Context, b string, args []string, in []byte) ([]byte, error) {
				joined := strings.Join(args, " ")
				if strings.Contains(joined, "-O forward") {
					forwards++
					if failure == "malformed" && forwards == 1 {
						return []byte("garbage\n"), nil
					}
					if (failure == "second" || failure == "cancel") && forwards == 2 {
						return nil, fmt.Errorf("known allocation failure")
					}
					if failure == "canceled" && forwards == 2 {
						return nil, context.Canceled
					}
				}
				if strings.Contains(joined, "-O cancel") && (failure == "cancel" || failure == "canceled") {
					return nil, fmt.Errorf("cancel failed")
				}
				if len(in) > 0 && failure == "probe" {
					return nil, fmt.Errorf("probe failed")
				}
				return run(ctx, b, args, in)
			}
			if _, e := m.Acquire(context.Background(), p, r, target); e == nil {
				t.Fatal("failure accepted")
			}
			retained := failure == "cancel" || failure == "canceled"
			if (len(m.Status()) > 0) != retained {
				t.Fatal(m.Status(), *calls)
			}
			if retained {
				for _, status := range m.Status() {
					if status != "route_unavailable" {
						t.Fatal(status)
					}
				}
				if _, e := m.Acquire(context.Background(), p, r, target); e == nil {
					t.Fatal("retained failed route reused")
				}
			}
			if !retained {
				found := false
				for _, call := range *calls {
					if strings.Contains(call, "-O cancel") {
						found = true
					}
				}
				if !found {
					t.Fatal(*calls)
				}
			}
		})
	}
}
func TestTunnelSessionAndMasterExit(t *testing.T) {
	m, p, r, target, _ := tunnelFixture(t)
	wrong := p
	wrong.Config.SessionKey = strings.Repeat("b", 64)
	if _, e := m.Acquire(context.Background(), wrong, r, target); e == nil {
		t.Fatal("wrong session accepted")
	}
	if _, e := m.Acquire(context.Background(), p, r, target); e != nil {
		t.Fatal(e)
	}
	for _, master := range m.Masters {
		close(master.done)
	}
	if _, e := m.Acquire(context.Background(), p, r, target); e == nil || !strings.Contains(e.Error(), "route_unavailable") {
		t.Fatal(e)
	}
}
func TestTunnelLocalBindRetries(t *testing.T) {
	m, p, r, target, calls := tunnelFixture(t)
	r.Host.MachineID = "peer"
	r.Host.HerdrSocket = target.HerdrSocket
	target.MachineID = "local"
	target.HerdrSocket = "/tmp/local-herdr.sock"
	attempts := 0
	m.Run = func(_ context.Context, _ string, args []string, _ []byte) ([]byte, error) {
		*calls = append(*calls, strings.Join(args, " "))
		if strings.Contains(strings.Join(args, " "), "-O forward") {
			attempts++
		}
		if strings.Contains(strings.Join(args, " "), "-O cancel") {
			return nil, nil
		}
		return nil, fmt.Errorf("bind failed")
	}
	if _, e := m.Acquire(context.Background(), p, r, target); e == nil || attempts != 3 {
		t.Fatal(e, attempts, *calls)
	}
}
func TestTunnelDirectNoSSH(t *testing.T) {
	m, p, r, target, _ := tunnelFixture(t)
	r.Host.MachineID = "local"
	target.MachineID = "local"
	target.HerdrSocket = r.Host.HerdrSocket
	m.Run = func(context.Context, string, []string, []byte) ([]byte, error) {
		t.Fatal("direct route ran SSH")
		return nil, nil
	}
	// A dead protocol endpoint must fail locally before any SSH call.
	r.Source.Descriptor = "parley://127.0.0.1:1/r"
	if _, e := m.Acquire(context.Background(), p, r, target); e == nil {
		t.Fatal("dead direct endpoint accepted")
	}
}

func TestTunnelUncertainTransport(t *testing.T) {
	m, p, r, target, _ := tunnelFixture(t)
	failure := io.EOF
	attempts := 0
	m.Run = func(context.Context, string, []string, []byte) ([]byte, error) { attempts++; return nil, failure }
	if _, e := m.Acquire(context.Background(), p, r, target); e == nil {
		t.Fatal("lost mux response accepted")
	}
	if len(m.Status()) != 1 || attempts != 2 {
		t.Fatal("uncertain forward disappeared", m.Status(), attempts)
	}
}

func TestTunnelCanceledSharedAcquisition(t *testing.T) {
	m, p, r, target, _ := tunnelFixture(t)
	if _, e := m.Acquire(context.Background(), p, r, target); e != nil {
		t.Fatal(e)
	}
	second := target
	second.TerminalID = "term2"
	m.Run = func(context.Context, string, []string, []byte) ([]byte, error) { return nil, context.Canceled }
	if _, e := m.Acquire(context.Background(), p, r, second); e == nil {
		t.Fatal("cancellation accepted")
	}
	for _, status := range m.Status() {
		if status != "ready" {
			t.Fatal("existing route poisoned by canceled caller", status)
		}
	}
}

func TestTunnelSeparateHerdrSessions(t *testing.T) {
	m, p, r, target, calls := tunnelFixture(t)
	if _, e := m.Acquire(context.Background(), p, r, target); e != nil {
		t.Fatal(e)
	}
	second := target
	second.HerdrSocket = "/tmp/other-herdr.sock"
	key, _ := SessionKey(second.HerdrSocket)
	other := p
	other.Machine.Session = "other"
	other.Config.HerdrSocket = second.HerdrSocket
	other.Config.SessionKey = key
	m.Masters["peer:"+key] = &Master{ControlPath: "/tmp/owned-other/master", Target: p.Machine.Target, done: make(chan struct{})}
	if _, e := m.Acquire(context.Background(), other, r, second); e != nil {
		t.Fatal(e)
	}
	n := 0
	for _, call := range *calls {
		if strings.Contains(call, "-O forward") {
			n++
		}
	}
	if n != 4 || len(m.Status()) != 2 {
		t.Fatal(n, m.Status(), *calls)
	}
}
func TestTunnelMasterLifetime(t *testing.T) {
	dir := t.TempDir()
	if e := os.WriteFile(filepath.Join(dir, "ssh"), []byte("#!/bin/sh\ncase \"$*\" in *'-O check'*) exit 0;; esac\nexec sleep 60\n"), 0700); e != nil {
		t.Fatal(e)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	m, p, r, target, _ := tunnelFixture(t)
	m.Masters = nil
	ctx, cancel := context.WithCancel(context.Background())
	if _, e := m.Acquire(ctx, p, r, target); e != nil {
		t.Fatal(e)
	}
	cancel()
	var master *Master
	for _, v := range m.Masters {
		master = v
	}
	if !masterAlive(master) {
		t.Fatal("Join context killed master")
	}
	if e := m.Close(); e != nil {
		t.Fatal(e)
	}
	if masterAlive(master) || master.Command.ProcessState == nil {
		t.Fatal("owned master not reaped")
	}
	if _, e := os.Stat(filepath.Dir(master.ControlPath)); !os.IsNotExist(e) {
		t.Fatal("private control root retained", e)
	}
}

func TestTunnelUncertainMuxExit(t *testing.T) {
	m, p, r, target, calls := tunnelFixture(t)
	failure := exec.Command("/bin/sh", "-c", "exit 255").Run()
	m.Run = func(_ context.Context, _ string, args []string, _ []byte) ([]byte, error) {
		*calls = append(*calls, strings.Join(args, " "))
		return nil, failure
	}
	if _, e := m.Acquire(context.Background(), p, r, target); e == nil {
		t.Fatal("lost mux response accepted")
	}
	if len(m.Status()) != 1 || len(*calls) != 2 || !strings.Contains((*calls)[1], "-O cancel") || !strings.Contains((*calls)[1], "-R 127.0.0.1:0:127.0.0.1:40001") {
		t.Fatal("uncertain candidate discarded", m.Status(), *calls)
	}
}
func TestTunnelLocalAmbiguousAllocation(t *testing.T) {
	for _, cancelFails := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelFails), func(t *testing.T) {
			m, p, r, target, calls := tunnelFixture(t)
			r.Host.MachineID = "peer"
			r.Host.HerdrSocket = target.HerdrSocket
			target.MachineID = "local"
			target.HerdrSocket = "/tmp/local-herdr.sock"
			attempts, cancels := 0, 0
			m.Run = func(_ context.Context, _ string, args []string, _ []byte) ([]byte, error) {
				*calls = append(*calls, strings.Join(args, " "))
				if strings.Contains(strings.Join(args, " "), "-O forward") {
					attempts++
					return nil, fmt.Errorf("lost forward response")
				}
				if strings.Contains(strings.Join(args, " "), "-O cancel") {
					cancels++
					if cancelFails {
						return nil, fmt.Errorf("lost cancel response")
					}
					return nil, nil
				}
				t.Fatal(args)
				return nil, nil
			}
			if _, e := m.Acquire(context.Background(), p, r, target); e == nil {
				t.Fatal("allocation failure accepted")
			}
			want := 3
			if cancelFails {
				want = 1
			}
			if attempts != want || cancels != want || (len(m.Status()) > 0) != cancelFails {
				t.Fatal(attempts, cancels, m.Status(), *calls)
			}
			for i := 0; i < len(*calls); i += 2 {
				if !strings.Contains((*calls)[i], "-O forward") || !strings.Contains((*calls)[i+1], "-O cancel") || strings.Split((*calls)[i], "-L ")[1] != strings.Split((*calls)[i+1], "-L ")[1] {
					t.Fatal("candidate not canceled before retry", *calls)
				}
			}
		})
	}
}

func TestTunnelAcquireReportsRetainedOwnership(t *testing.T) {
	for _, retained := range []bool{false, true} {
		t.Run(fmt.Sprint(retained), func(t *testing.T) {
			manager, peer, room, target, _ := tunnelFixture(t)
			manager.Run = func(_ context.Context, _ string, args []string, _ []byte) ([]byte, error) {
				if strings.Contains(strings.Join(args, " "), "-O cancel") && !retained {
					return nil, nil
				}
				return nil, fmt.Errorf("generic mux exit255")
			}
			_, err := manager.Acquire(context.Background(), peer, room, target)
			if err == nil {
				t.Fatal("failed acquisition passed")
			}
			var uncertain *AcquisitionError
			if errors.As(err, &uncertain) != retained {
				t.Fatal(err, retained)
			}
			if retained && len(manager.leases) != 1 {
				t.Fatal("uncertain ownership lost")
			}
			if !retained && len(manager.leases) != 0 {
				t.Fatal("confirmed cleanup kept lease")
			}
		})
	}
}
