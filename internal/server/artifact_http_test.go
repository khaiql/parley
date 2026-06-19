package server

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/khaiql/parley/internal/artifact"
)

func TestArtifactHTTPUploadStagesArtifact(t *testing.T) {
	srv := newInternalTestServer(t)
	httpSrv := httptest.NewServer(srv.ArtifactHandler())
	defer httpSrv.Close()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "trace.json")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write([]byte("trace bytes")); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, httpSrv.URL+"/rooms/room-1/artifacts/staged?participant=alice", &body)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var meta artifact.Metadata
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if meta.ID == "" || meta.Name != "trace.json" || meta.Size != int64(len("trace bytes")) || meta.SHA256 == "" {
		t.Fatalf("metadata = %#v, want staged trace metadata", meta)
	}
}

func TestArtifactHTTPFetchReturnsCommittedBytes(t *testing.T) {
	srv := newInternalTestServer(t)
	httpSrv := httptest.NewServer(srv.ArtifactHandler())
	defer httpSrv.Close()
	staged, err := srv.artifacts.Stage("alice", "trace.json", []byte("trace bytes"))
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if _, err := srv.artifacts.Commit("alice", []string{staged.ID}); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	resp, err := http.Get(httpSrv.URL + "/rooms/room-1/artifacts/" + staged.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if resp.StatusCode != http.StatusOK || string(data) != "trace bytes" {
		t.Fatalf("status/data = %d %q, want 200 trace bytes", resp.StatusCode, data)
	}
}

func TestArtifactHTTPFetchMissingReturnsNotFound(t *testing.T) {
	srv := newInternalTestServer(t)
	httpSrv := httptest.NewServer(srv.ArtifactHandler())
	defer httpSrv.Close()

	resp, err := http.Get(httpSrv.URL + "/rooms/room-1/artifacts/art_missing")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Error.Code != "artifact_unavailable" {
		t.Fatalf("error = %#v, want artifact_unavailable", payload.Error)
	}
}

func TestArtifactHTTPCleanupStagedRemovesOnlyParticipantArtifacts(t *testing.T) {
	srv := newInternalTestServer(t)
	httpSrv := httptest.NewServer(srv.ArtifactHandler())
	defer httpSrv.Close()
	alice, err := srv.artifacts.Stage("alice", "alice.txt", []byte("alice"))
	if err != nil {
		t.Fatalf("Stage alice: %v", err)
	}
	bob, err := srv.artifacts.Stage("bob", "bob.txt", []byte("bob"))
	if err != nil {
		t.Fatalf("Stage bob: %v", err)
	}

	req, err := http.NewRequest(http.MethodDelete, httpSrv.URL+"/rooms/room-1/artifacts/staged?participant=alice", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if _, err := srv.artifacts.Commit("alice", []string{alice.ID}); !artifact.IsCode(err, "artifact_unavailable") {
		t.Fatalf("alice commit err = %v, want artifact_unavailable", err)
	}
	if _, err := srv.artifacts.Commit("bob", []string{bob.ID}); err != nil {
		t.Fatalf("bob commit after alice cleanup: %v", err)
	}
}

func TestArtifactHTTPWrongRoomReturnsNotFound(t *testing.T) {
	srv := newInternalTestServer(t)
	httpSrv := httptest.NewServer(srv.ArtifactHandler())
	defer httpSrv.Close()

	resp, err := http.Get(httpSrv.URL + "/rooms/wrong-room/artifacts/art_missing")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Error.Code != "artifact_unavailable" {
		t.Fatalf("error = %#v, want artifact_unavailable", payload.Error)
	}
}
