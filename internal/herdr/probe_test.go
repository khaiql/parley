package herdr

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/khaiql/parley/internal/eventlog"
	"github.com/khaiql/parley/internal/server"
)

func TestArtifactProbe(t *testing.T) {
	// Every repository method panics, so any probe mutation or fetch fails this test.
	log := eventlog.New(t.TempDir() + "/events.jsonl")
	srv, err := server.New("127.0.0.1:0", server.Config{RoomID: "probe-room", Log: log, ArtifactStore: untouchedRepository{}})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	h := httptest.NewServer(srv.ArtifactHandler())
	defer h.Close()
	if err := ProbeArtifact(context.Background(), h.URL+"/rooms/probe-room/artifacts", "probe-room"); err != nil {
		t.Fatal(err)
	}
	for _, behavior := range []string{"200", "404", "400", "wrong", "extra", "multiple", "redirect", "oversize"} {
		t.Run(behavior, func(t *testing.T) {
			fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				status := 400
				body := `{"error":{"code":"bad_request","message":"participant query parameter is required"}}`
				switch behavior {
				case "200":
					status = 200
				case "404":
					status = 404
				case "wrong":
					body = `{"error":{"code":"wrong","message":"wrong"}}`
				case "extra":
					body = `{"error":{"code":"bad_request","message":"participant query parameter is required","extra":true}}`
				case "multiple":
					body += body
				case "redirect":
					status = 302
					w.Header().Set("Location", h.URL+r.URL.Path)
				case "oversize":
					body += strings.Repeat(" ", 65536)
				}
				w.WriteHeader(status)
				_, _ = w.Write([]byte(body))
			}))
			defer fake.Close()
			if ProbeArtifact(context.Background(), fake.URL+"/rooms/probe-room/artifacts", "probe-room") == nil {
				t.Fatal("accepted impostor")
			}
		})
	}
}
func TestArtifactProbeInvalidEndpoint(t *testing.T) {
	for _, url := range []string{"http://127.0.0.1:0/rooms/r/artifacts", "http://127.0.0.1:65536/rooms/r/artifacts", "http://localhost:80/rooms/r/artifacts", "http://127.0.0.1:80/rooms/x/artifacts", "http://127.0.0.1:80/rooms/r/artifacts?", "http://127.0.0.1:80/rooms/r/artifacts#"} {
		if ProbeArtifact(context.Background(), url, "r") == nil {
			t.Fatal(url)
		}
	}
}

type untouchedRepository struct{ server.ArtifactRepository }

func TestArtifactProbeRequestsAndCancellation(t *testing.T) {
	count := 0
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		body, _ := io.ReadAll(r.Body)
		if r.Method != "POST" || r.URL.RawQuery != "" || len(body) != 0 {
			t.Error("mutating probe request", r.Method, r.URL, string(body))
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if r.URL.Path == "/rooms/r/artifacts/staged" {
			w.WriteHeader(400)
			_, _ = io.WriteString(w, `{"error":{"code":"bad_request","message":"participant query parameter is required"}}`)
		} else {
			w.WriteHeader(404)
			_, _ = io.WriteString(w, `{"error":{"code":"artifact_unavailable","message":"artifact route is not available"}}`)
		}
	}))
	defer h.Close()
	if e := ProbeArtifact(context.Background(), h.URL+"/rooms/r/artifacts", "r"); e != nil || count != 2 {
		t.Fatal(e, count)
	}
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer slow.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if e := ProbeArtifact(ctx, slow.URL+"/rooms/r/artifacts", "r"); e == nil {
		t.Fatal("timeout accepted")
	}
}
func TestArtifactProbeOwnedRPC(t *testing.T) {
	h := testHost(t)
	claim := RPCRequest{Operation: "claim-controller", Session: "agents", SessionKey: h.Config.SessionKey, ControllerID: "controller", MachineID: "peer"}
	if _, e := h.Dispatch(context.Background(), claim); e != nil {
		t.Fatal(e)
	}
	log := eventlog.New(t.TempDir() + "/events.jsonl")
	srv, e := server.New("127.0.0.1:0", server.Config{RoomID: "r", Log: log, ArtifactStore: untouchedRepository{}})
	if e != nil {
		t.Fatal(e)
	}
	defer srv.Close()
	httpServer := httptest.NewServer(srv.ArtifactHandler())
	defer httpServer.Close()
	endpoints := Endpoints{Descriptor: "parley://" + srv.Addr() + "/r", ArtifactURL: httpServer.URL + "/rooms/r/artifacts"}
	req := RPCRequest{Operation: "probe-route", Session: "agents", SessionKey: h.Config.SessionKey, ControllerID: "controller", RoomID: "r", Endpoints: &endpoints}
	if _, e := h.Dispatch(context.Background(), req); e != nil {
		t.Fatal(e)
	}
	req.ControllerID = "other"
	if _, e := h.Dispatch(context.Background(), req); e == nil {
		t.Fatal("unowned probe accepted")
	}
}

func TestArtifactProbeRegistryNumericPorts(t *testing.T) {
	h := testHost(t)
	owner := SessionConfig{MachineID: "peer", HerdrSocket: h.Config.HerdrSocket}
	room := Room{ID: "r", HostSessionID: "psn_host", Host: AgentTarget{MachineID: "self", HerdrSocket: h.Config.HerdrSocket, PaneID: "p1", WorkspaceID: "w1", TerminalID: "term", Kind: "codex", NativeSession: "native"}, Source: Endpoints{Descriptor: "parley://127.0.0.1:4001/r"}}
	for _, port := range []string{"0", "65536", "not-a-port"} {
		room.Source.ArtifactURL = "http://127.0.0.1:" + port + "/rooms/r/artifacts"
		if _, e := h.validRoom(room, owner); e == nil {
			t.Fatal("registry accepted invalid artifact port", port)
		}
	}
}
