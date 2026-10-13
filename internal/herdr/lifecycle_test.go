package herdr

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
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
