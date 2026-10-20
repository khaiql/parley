package herdr

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestWatchRequestsRetriesQueueSSHFailure(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\nprintf '%s\\n' '" + testEventAck[:len(testEventAck)-1] + "'\nexec /bin/sleep 10\n"
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	key, _ := SessionKey("/tmp/a.sock")
	peer := Peer{Machine: Machine{ID: "remote", Target: "host", Session: "agents"}, Binary: "/opt/parley", Config: SessionConfig{SessionKey: key, ControllerID: "controller"}}
	calls := 0
	client := &Client{LocalMachineID: "local", Run: func(ctx context.Context, _ string, _ []string, _ []byte) ([]byte, error) {
		calls++
		if calls == 1 {
			return RunProcess(ctx, "/bin/sh", []string{"-c", "exit 255"}, nil)
		}
		cancel()
		return []byte("[]"), nil
	}}
	broker := Broker{Store: NewStore(t.TempDir()), Client: client}
	err := client.WatchRequests(ctx, peer, broker.Drain)
	if calls != 2 || !errors.Is(err, context.Canceled) {
		t.Fatalf("queue reads = %d, watcher = %v", calls, err)
	}
}

func TestCallClassifiesSSHFailureWithoutRetryingHelperErrors(t *testing.T) {
	key, _ := SessionKey("/tmp/a.sock")
	peer := Peer{Machine: Machine{ID: "remote", Target: "host", Session: "agents"}, Binary: "/opt/parley"}
	request := RPCRequest{Operation: "requests", SessionKey: key, ControllerID: "controller"}
	for _, status := range []int{1, 255} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			client := Client{Run: func(ctx context.Context, _ string, _ []string, _ []byte) ([]byte, error) {
				return RunProcess(ctx, "/bin/sh", []string{"-c", fmt.Sprintf("exit %d", status)}, nil)
			}}
			_, err := client.Call(context.Background(), peer, request)
			if err == nil || isEventTransportError(err) != (status == 255) {
				t.Fatalf("SSH exit %d: %v", status, err)
			}
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	client := Client{Run: func(ctx context.Context, _ string, _ []string, _ []byte) ([]byte, error) {
		return RunProcess(ctx, "/bin/sleep", []string{"10"}, nil)
	}}
	_, err := client.Call(ctx, peer, request)
	if !errors.Is(err, context.DeadlineExceeded) || !isEventTransportError(err) {
		t.Fatalf("queue timeout is not recoverable: %v", err)
	}
}

func TestOpenEventsBoundsSSHConnectionLiveness(t *testing.T) {
	dir := t.TempDir()
	script := `#!/bin/sh
for option in ConnectTimeout=5 ServerAliveInterval=30 ServerAliveCountMax=3; do
  case " $* " in
    *" -o $option "*) ;;
    *) printf '%s\n' '{"id":"parley-requests","error":{"code":"missing_liveness"}}'; exit 1 ;;
  esac
done
printf '%s\n' '{"id":"parley-requests","result":{"type":"subscription_started"}}'
exec /bin/sleep 10
`
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	key, _ := SessionKey("/tmp/a.sock")
	peer := Peer{Machine: Machine{ID: "remote", Target: "host", Session: "agents"}, Binary: "/opt/parley", Config: SessionConfig{SessionKey: key, ControllerID: "controller"}}
	stream, err := (&Client{}).OpenEvents(ctx, peer)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	started := false
	err = ReadEvents(ctx, stream, func() error { started = true; cancel(); return nil }, func() error { return nil })
	if !started || !errors.Is(err, context.Canceled) {
		t.Fatalf("SSH liveness configuration rejected: started=%v, err=%v", started, err)
	}
}
