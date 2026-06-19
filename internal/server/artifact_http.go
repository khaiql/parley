package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/khaiql/parley/internal/artifact"
	"github.com/khaiql/parley/internal/protocol"
)

func (s *Server) ArtifactHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/rooms/", s.handleArtifactHTTP)
	return mux
}

func (s *Server) handleArtifactHTTP(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/rooms/"), "/")
	if len(parts) < 3 || parts[0] != s.cfg.RoomID || parts[1] != "artifacts" {
		writeArtifactHTTPError(w, http.StatusNotFound, "artifact_unavailable", "artifact route is not available")
		return
	}

	switch {
	case r.Method == http.MethodPost && len(parts) == 3 && parts[2] == "staged":
		s.handleArtifactUpload(w, r)
	case r.Method == http.MethodDelete && len(parts) == 3 && parts[2] == "staged":
		s.handleArtifactCleanup(w, r)
	case r.Method == http.MethodGet && len(parts) == 3 && parts[2] != "":
		s.handleArtifactFetch(w, r, parts[2])
	default:
		writeArtifactHTTPError(w, http.StatusNotFound, "artifact_unavailable", "artifact route is not available")
	}
}

func (s *Server) handleArtifactUpload(w http.ResponseWriter, r *http.Request) {
	participant := strings.TrimSpace(r.URL.Query().Get("participant"))
	if participant == "" {
		writeArtifactHTTPError(w, http.StatusBadRequest, "bad_request", "participant query parameter is required")
		return
	}
	reader, err := r.MultipartReader()
	if err != nil {
		writeArtifactHTTPError(w, http.StatusBadRequest, "artifact_upload_failed", err.Error())
		return
	}
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			writeArtifactHTTPError(w, http.StatusBadRequest, "artifact_upload_failed", "multipart field file is required")
			return
		}
		if err != nil {
			writeArtifactHTTPError(w, http.StatusBadRequest, "artifact_upload_failed", err.Error())
			return
		}
		if part.FormName() != "file" {
			_ = part.Close()
			continue
		}
		defer part.Close()
		meta, err := s.artifacts.StageReader(participant, part.FileName(), -1, part)
		if err != nil {
			writeArtifactHTTPErrorForArtifact(w, err)
			return
		}
		writeArtifactJSON(w, meta)
		return
	}
}

func (s *Server) handleArtifactCleanup(w http.ResponseWriter, r *http.Request) {
	participant := strings.TrimSpace(r.URL.Query().Get("participant"))
	if participant == "" {
		writeArtifactHTTPError(w, http.StatusBadRequest, "bad_request", "participant query parameter is required")
		return
	}
	if err := s.artifacts.CleanupStaged(participant); err != nil {
		writeArtifactHTTPErrorForArtifact(w, err)
		return
	}
	writeArtifactJSON(w, struct {
		Status string `json:"status"`
	}{Status: "cleaned"})
}

func (s *Server) handleArtifactFetch(w http.ResponseWriter, _ *http.Request, id string) {
	s.artifactTxMu.Lock()
	rc, meta, err := s.artifacts.Open(id)
	s.artifactTxMu.Unlock()
	if err != nil {
		writeArtifactHTTPErrorForArtifact(w, err)
		return
	}
	defer rc.Close()

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, strings.ReplaceAll(meta.Name, `"`, "")))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, rc)
}

func artifactErrorResponse(err error) protocol.Response {
	var artifactErr artifact.Error
	if errors.As(err, &artifactErr) {
		return errorResponse(artifactErr.Code, artifactErr.Message)
	}
	return errorResponse("artifact_unavailable", err.Error())
}

func writeArtifactHTTPErrorForArtifact(w http.ResponseWriter, err error) {
	var artifactErr artifact.Error
	if errors.As(err, &artifactErr) {
		status := http.StatusBadRequest
		if artifactErr.Code == "artifact_unavailable" {
			status = http.StatusNotFound
		}
		writeArtifactHTTPError(w, status, artifactErr.Code, artifactErr.Message)
		return
	}
	writeArtifactHTTPError(w, http.StatusInternalServerError, "artifact_unavailable", err.Error())
}

func writeArtifactHTTPError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Error protocol.Error `json:"error"`
	}{Error: protocol.Error{Code: code, Message: message}})
}

func writeArtifactJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(v)
}
