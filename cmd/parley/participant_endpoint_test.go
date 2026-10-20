package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/khaiql/parley/internal/adapter"
	"github.com/khaiql/parley/internal/artifact"
	"github.com/khaiql/parley/internal/eventlog"
	"github.com/khaiql/parley/internal/model"
	parleyRuntime "github.com/khaiql/parley/internal/runtime"
	"github.com/khaiql/parley/internal/server"
)

func TestArtifactEndpointOverrideValidation(t *testing.T) {
	cases := []struct {
		raw   string
		valid bool
	}{
		{"http://127.0.0.1:49002/rooms/room-1/artifacts", true},
		{"http://[::1]:49002/rooms/room-1/artifacts", true},
		{"http://tunnel.example:65535/rooms/room-1/artifacts", true},
		{"https://127.0.0.1:49002/rooms/room-1/artifacts", false},
		{"http://user:secret@127.0.0.1:49002/rooms/room-1/artifacts", false},
		{"http://127.0.0.1:49002/rooms/other/artifacts", false},
		{"http://127.0.0.1:49002/rooms/room-1/artifacts/", false},
		{"http://127.0.0.1:49002/rooms/room-1/artifacts?x=1", false},
		{"http://127.0.0.1:49002/rooms/room-1/artifacts?", false},
		{"http://127.0.0.1:49002/rooms/room-1/artifacts#x", false},
		{"http://127.0.0.1:49002/rooms/room-1/artifacts#", false},
		{"http://127.0.0.1:0/rooms/room-1/artifacts", false},
		{"http://127.0.0.1:65536/rooms/room-1/artifacts", false},
		{"http://127.0.0.1/rooms/room-1/artifacts", false},
		{"http://127.0.0.1:bad/rooms/room-1/artifacts", false},
		{"http://:49002/rooms/room-1/artifacts", false},
		{"http:opaque", false},
	}
	for _, c := range cases {
		t.Run(c.raw, func(t *testing.T) {
			if err := validateArtifactEndpoint(c.raw, "room-1"); (err == nil) != c.valid {
				t.Fatalf("error=%v, valid=%v", err, c.valid)
			}
		})
	}
}

func TestArtifactEndpointOverrideInvalidJoinDoesNotLaunch(t *testing.T) {
	useParleyHome(t)
	original := launchParticipantDaemon
	t.Cleanup(func() { launchParticipantDaemon = original })
	launchParticipantDaemon = func(participantDaemonConfig) (int, error) {
		t.Fatal("invalid endpoint reached daemon launch")
		return 0, nil
	}
	out, err := executeForTest("join", "parley://127.0.0.1:1234/room-1", "--name", "alice", "--artifact-endpoint", "http://127.0.0.1:49002/rooms/other/artifacts")
	if err == nil {
		t.Fatal("invalid endpoint accepted")
	}
	assertJSONErrorCode(t, out, "invalid_artifact_endpoint")
}

func TestArtifactEndpointOverrideHiddenDaemonRejectsInvalid(t *testing.T) {
	useParleyHome(t)
	cmd := participantDaemonCmd()
	cmd.SetArgs([]string{"--descriptor", "parley://127.0.0.1:1234/room-1", "--name", "alice", "--artifact-endpoint", "http://127.0.0.1:49002/rooms/other/artifacts"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "invalid artifact endpoint") {
		t.Fatalf("hidden daemon error = %v, want endpoint validation before TCP dial", err)
	}
}

// Exercise public join, detached daemon argv, adapter initialization, and real HTTP bytes.
func TestArtifactEndpointOverrideRemappedBytes(t *testing.T) {
	p := useShortParleyHome(t)
	roomDir, err := p.EnsureRoomDir("room-1")
	if err != nil {
		t.Fatal(err)
	}
	// Hold a non-listening TCP port so the advertised endpoint cannot accept HTTP.
	unavailable, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM, syscall.IPPROTO_TCP)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Close(unavailable)
	if err := syscall.Bind(unavailable, &syscall.SockaddrInet4{Addr: [4]byte{127, 0, 0, 1}}); err != nil {
		t.Fatal(err)
	}
	bound, err := syscall.Getsockname(unavailable)
	if err != nil {
		t.Fatal(err)
	}
	advertisedPort := bound.(*syscall.SockaddrInet4).Port
	probe, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", advertisedPort), 100*time.Millisecond)
	if err == nil {
		probe.Close()
		t.Fatal("advertised artifact TCP port unexpectedly reachable")
	}
	srv, err := server.New("127.0.0.1:0", server.Config{
		RoomID: "room-1", Topic: "remapped artifacts", Log: eventlog.New(filepath.Join(roomDir, "events.jsonl")),
		ArtifactStore: artifact.NewStore(roomDir), ArtifactLocalPort: advertisedPort,
		ArtifactPath: "/rooms/room-1/artifacts", ArtifactLimits: artifact.DefaultLimits(),
	})
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve()
	defer srv.Close()
	var mu sync.Mutex
	var requests []string
	handler := srv.ArtifactHandler()
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.Path)
		mu.Unlock()
		handler.ServeHTTP(w, r)
	}))
	defer httpServer.Close()
	endpoint := httpServer.URL + "/rooms/room-1/artifacts"
	binary := filepath.Join(t.TempDir(), "parley")
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	run := func(args ...string) []byte {
		t.Helper()
		out, err := exec.Command(binary, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("parley %v: %v\n%s", args, err, out)
		}
		return out
	}
	descriptor := fmt.Sprintf("parley://127.0.0.1:%d/room-1", srv.Port())
	for _, c := range []struct{ name, endpoint, want string }{
		{"remapped", endpoint, endpoint},
		{"default", "", fmt.Sprintf("http://127.0.0.1:%d/rooms/room-1/artifacts", advertisedPort)},
	} {
		args := []string{"join", descriptor, "--name", c.name}
		if c.endpoint != "" {
			args = append(args, "--artifact-endpoint", c.endpoint)
		}
		var joined struct {
			PID         int          `json:"pid"`
			Participant adapter.Meta `json:"participant"`
		}
		if err := json.Unmarshal(run(args...), &joined); err != nil {
			t.Fatal(err)
		}
		process, err := os.FindProcess(joined.PID)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = process.Kill() })
		if joined.Participant.ArtifactEndpoint != c.want {
			t.Fatalf("%s endpoint = %q, want %q", c.name, joined.Participant.ArtifactEndpoint, c.want)
		}
	}
	content := []byte("remapped artifact bytes\x00\xff\n")
	input := filepath.Join(t.TempDir(), "payload.bin")
	if err := os.WriteFile(input, content, 0600); err != nil {
		t.Fatal(err)
	}
	var sent struct {
		Status string        `json:"status"`
		Events []model.Event `json:"events"`
	}
	if err := json.Unmarshal(run("send", "--room", "room-1", "--name", "remapped", "--file", input, "file through remapped endpoint"), &sent); err != nil {
		t.Fatal(err)
	}
	if sent.Status != "sent" {
		t.Fatalf("send = %#v", sent)
	}
	var metadata model.ArtifactMetadata
	for _, ev := range sent.Events {
		if ev.Type != model.EventMessage {
			continue
		}
		data, err := json.Marshal(ev.Payload)
		if err != nil {
			t.Fatal(err)
		}
		var payload model.MessagePayload
		if err := json.Unmarshal(data, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Text != "file through remapped endpoint" || len(payload.Artifacts) != 1 {
			t.Fatalf("message = %#v", payload)
		}
		metadata = payload.Artifacts[0]
	}
	wantSHA := fmt.Sprintf("%x", sha256.Sum256(content))
	if metadata.ID == "" || metadata.SHA256 != wantSHA || metadata.Size != int64(len(content)) {
		t.Fatalf("artifact metadata = %#v, want SHA %s", metadata, wantSHA)
	}
	output := filepath.Join(t.TempDir(), "fetched.bin")
	run("artifact", "fetch", "--room", "room-1", "--name", "remapped", metadata.ID, "--out", output)
	fetched, err := os.ReadFile(output)
	if err != nil || !bytes.Equal(fetched, content) || fmt.Sprintf("%x", sha256.Sum256(fetched)) != wantSHA {
		t.Fatalf("fetched bytes = %q, error=%v", fetched, err)
	}
	mu.Lock()
	defer mu.Unlock()
	wantRequests := []string{"POST /rooms/room-1/artifacts/staged", "GET /rooms/room-1/artifacts/" + metadata.ID}
	if len(requests) != len(wantRequests) {
		t.Fatalf("HTTP requests = %v", requests)
	}
	for i, want := range wantRequests {
		if requests[i] != want {
			t.Fatalf("HTTP requests = %v, want %v", requests, wantRequests)
		}
	}
	store, err := parleyRuntime.ParticipantStore(p, "room-1", "remapped")
	if err != nil {
		t.Fatal(err)
	}
	meta, err := store.LoadMeta()
	if err != nil || meta.ArtifactEndpoint != endpoint {
		t.Fatalf("persisted endpoint = %q, error=%v", meta.ArtifactEndpoint, err)
	}
}
