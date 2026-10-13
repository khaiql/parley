package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHerdrStartupNamespaceAndOwner(t *testing.T) {
	root := t.TempDir()
	h := HostSession{Config: SessionConfig{HerdrSocket: "/tmp/native-a.sock"}, ConfigDir: filepath.Join(root, "a"), Store: NewStore(filepath.Join(root, "state-a"))}
	h.Config.SessionKey, _ = SessionKey(h.Config.HerdrSocket)
	sibling := HostSession{Config: SessionConfig{HerdrSocket: "/tmp/native-b.sock"}, ConfigDir: filepath.Join(root, "b"), Store: NewStore(filepath.Join(root, "state-b"))}
	sibling.Config.SessionKey, _ = SessionKey(sibling.Config.HerdrSocket)
	c := &Client{RuntimeRoot: filepath.Join(root, "runtime")}
	owner, err := h.Setup(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	again, err := h.Setup(context.Background(), c)
	if err != nil || again != owner || owner.ControllerID == "local" {
		t.Fatalf("%+v %+v %v", owner, again, err)
	}
	if err = sibling.Startup(context.Background(), "/no/executable", "sibling"); err != nil {
		t.Fatal(err)
	}
	if _, err = sibling.CoordinatorConfig(); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	other, err := sibling.Setup(context.Background(), c)
	if err != nil || other.ControllerID == owner.ControllerID {
		t.Fatalf("%+v %v", other, err)
	}
	a, _ := h.CoordinatorSocket()
	b, _ := sibling.CoordinatorSocket()
	t.Cleanup(func() { os.RemoveAll(filepath.Dir(a)); os.RemoveAll(filepath.Dir(b)) })
	if a == b || len(a) > 103 {
		t.Fatal(a, b)
	}
}
func TestHerdrSetupParticipantConflict(t *testing.T) {
	root := t.TempDir()
	h := HostSession{Config: SessionConfig{HerdrSocket: "/tmp/native.sock"}, ConfigDir: root, Store: NewStore(filepath.Join(root, "state"))}
	h.Config.SessionKey, _ = SessionKey(h.Config.HerdrSocket)
	owner := h.Config
	owner.Role = "participant"
	owner.MachineID = "remote"
	owner.ControllerID = "controller"
	if err := writeJSON(filepath.Join(root, "controller.json"), owner); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Setup(context.Background(), &Client{RuntimeRoot: root}); err == nil {
		t.Fatal("participant became controller")
	}
	if err := h.Startup(context.Background(), "/no/executable", "native"); err != nil {
		t.Fatal(err)
	}
}
func TestFormPendingKeepsRequest(t *testing.T) {
	h, r, root := brokerFixture(t)
	host := HostSession{Config: SessionConfig{HerdrSocket: h.Client.HerdrSocket}, ConfigDir: filepath.Join(root, "config"), Store: h.Store}
	host.Config.SessionKey, _ = SessionKey(host.Config.HerdrSocket)
	owner := host.Config
	owner.Role = "participant"
	owner.MachineID = "local"
	owner.ControllerID = "controller"
	if err := privateDir(host.ConfigDir); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(host.ConfigDir, "controller.json"), owner); err != nil {
		t.Fatal(err)
	}
	if err := h.Store.Enqueue(r); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	response, err := host.WaitOutcome(ctx, r.ID)
	pending, pendingErr := h.Store.Pending()
	if err != nil || response.Status != "pending" || pendingErr != nil || len(pending) != 1 {
		t.Fatal(response, err, pending, pendingErr)
	}
}

func TestLaunchHelperChild(t *testing.T) {
	if os.Getenv("PARLEY_LAUNCH_CHILD") != "1" {
		return
	}
	path := os.Getenv("PARLEY_LAUNCH_SOCKET")
	file, err := os.OpenFile(path+".count", os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		os.Exit(2)
	}
	_, _ = file.WriteString("child\n")
	_ = file.Close()
	listener, err := net.Listen("unix", path)
	if err != nil {
		os.Exit(3)
	}
	time.AfterFunc(500*time.Millisecond, func() { _ = listener.Close() })
	for {
		conn, err := listener.Accept()
		if err != nil {
			break
		}
		_ = conn.Close()
	}
	os.Exit(0)
}
func TestHerdrStartupLaunchOnce(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "hl-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	path := filepath.Join(root, "broker")
	t.Setenv("PARLEY_LAUNCH_CHILD", "1")
	t.Setenv("PARLEY_LAUNCH_SOCKET", path)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"-test.run=^TestLaunchHelperChild$"}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err = LaunchHelper(ctx, binary, args, path); err != nil {
		t.Fatal(err)
	}
	if err = LaunchHelper(ctx, binary, args, path); err != nil {
		t.Fatal(err)
	}
	count, err := os.ReadFile(path + ".count")
	if err != nil || string(count) != "child\n" {
		t.Fatalf("%s %v", count, err)
	}
	// Child exits itself; no persisted PID or unrelated process is used for cleanup.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("unix", path, 10*time.Millisecond)
		if err != nil {
			return
		}
		_ = conn.Close()
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("owned child did not exit")
}

func TestHerdrControllerRoleClaimOrders(t *testing.T) {
	for _, first := range []string{"setup", "remote"} {
		t.Run(first, func(t *testing.T) {
			root := t.TempDir()
			host := HostSession{Config: SessionConfig{HerdrSocket: "/tmp/roles.sock"}, ConfigDir: filepath.Join(root, "config"), Store: NewStore(filepath.Join(root, "state"))}
			host.Config.SessionKey, _ = SessionKey(host.Config.HerdrSocket)
			claim := RPCRequest{Operation: "claim-controller", Session: "native", SessionKey: host.Config.SessionKey, ControllerID: "remote-owner", MachineID: "remote"}
			client := &Client{RuntimeRoot: root}
			if first == "setup" {
				owner, err := host.Setup(context.Background(), client)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = host.Dispatch(context.Background(), claim); err == nil || err.Error() != "controller_conflict" {
					t.Fatal("remote claimed coordinator", err)
				}
				retained, err := host.CoordinatorConfig()
				if err != nil || retained != owner {
					t.Fatal(retained, err)
				}
				if _, err = os.Stat(filepath.Join(host.ConfigDir, "controller.json")); !errors.Is(err, os.ErrNotExist) {
					t.Fatal(err)
				}
			} else {
				if _, err := host.Dispatch(context.Background(), claim); err != nil {
					t.Fatal(err)
				}
				if _, err := host.Setup(context.Background(), client); err == nil || err.Error() != "controller_conflict" {
					t.Fatal("setup claimed participant", err)
				}
				if _, err := host.CoordinatorConfig(); !errors.Is(err, os.ErrNotExist) {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestCoordinateDisableDuringRemoteRefresh(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "hc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	native := filepath.Join(root, "native.sock")
	listener, err := net.Listen("unix", native)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	subscribed := make(chan struct{})
	readerClosed := make(chan struct{})
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		var request any
		if json.NewDecoder(conn).Decode(&request) != nil {
			return
		}
		_, _ = io.WriteString(conn, `{"id":"parley-requests","result":{"type":"subscription_started"}}`+"\n")
		close(subscribed)
		_, _ = io.Copy(io.Discard, conn)
		close(readerClosed)
	}()
	host := HostSession{Config: SessionConfig{HerdrSocket: native}, ConfigDir: filepath.Join(root, "config"), Store: NewStore(filepath.Join(root, "state"))}
	host.Config.SessionKey, _ = SessionKey(native)
	blocked := make(chan struct{})
	remoteCanceled := make(chan struct{})
	client := &Client{HerdrBinary: "herdr", RuntimeRoot: filepath.Join(root, "runtime"), Run: func(ctx context.Context, _ string, args []string, _ []byte) ([]byte, error) {
		switch strings.Join(args, " ") {
		case "machine list --json":
			return []byte(`[{"id":"peer","target":"host","session":"agents","enabled":true}]`), nil
		case "plugin list --json":
			return []byte(`{"result":{"plugins":[{"plugin_id":"parley","enabled":false}]}}`), nil
		case "--machine peer plugin list --json":
			close(blocked)
			<-ctx.Done()
			close(remoteCanceled)
			return nil, ctx.Err()
		default:
			return nil, fmt.Errorf("unexpected native command: %v", args)
		}
	}}
	if _, err = host.Setup(context.Background(), client); err != nil {
		t.Fatal(err)
	}
	path, err := host.CoordinatorSocket()
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(filepath.Dir(path))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- host.Coordinate(ctx, client) }()
	// Cleanup also releases the deliberately blocked operation on the RED version.
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("coordinator cleanup blocked")
		}
	}()
	for _, ready := range []chan struct{}{blocked, subscribed} {
		select {
		case <-ready:
		case <-time.After(time.Second):
			t.Fatal("fixture not ready")
		}
	}
	select {
	case err = <-done:
		done <- err
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("local disable waited for blocked remote refresh")
	}
	for _, closed := range []chan struct{}{remoteCanceled, readerClosed} {
		select {
		case <-closed:
		case <-time.After(time.Second):
			t.Fatal("owned operation survived disable")
		}
	}
	if _, err = os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("owned broker socket survived", err)
	}
	// The native server is outside coordinator ownership and remains reachable.
	conn, err := net.DialTimeout("unix", native, time.Second)
	if err != nil {
		t.Fatal("native server was closed", err)
	}
	_ = conn.Close()
}
