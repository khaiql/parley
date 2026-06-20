package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/khaiql/parley/internal/adapter"
	"github.com/khaiql/parley/internal/artifact"
	"github.com/khaiql/parley/internal/descriptor"
	parleyRuntime "github.com/khaiql/parley/internal/runtime"
)

func deriveArtifactEndpoint(desc descriptor.Descriptor, room parleyRuntime.RoomRuntime) string {
	if room.ArtifactLocalPort == 0 {
		return ""
	}
	path := room.ArtifactPath
	if path == "" {
		path = artifactHTTPPath(desc.RoomID)
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return "http://" + net.JoinHostPort(desc.Host, fmt.Sprintf("%d", room.ArtifactLocalPort)) + path
}

func (rt *participantAdapterRuntime) uploadArtifacts(files []string) ([]string, []adapter.ArtifactFileResult, error) {
	if len(files) == 0 {
		return nil, nil, nil
	}
	infos, preflightResults, err := preflightArtifactFiles(files)
	if err != nil {
		return nil, preflightResults, err
	}
	type uploadResult struct {
		index int
		meta  artifact.Metadata
		err   error
	}
	concurrency := 4
	if len(files) < concurrency {
		concurrency = len(files)
	}
	sem := make(chan struct{}, concurrency)
	results := make(chan uploadResult, len(files))
	var wg sync.WaitGroup
	for i, info := range infos {
		wg.Add(1)
		go func(index int, filePath string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			meta, err := rt.uploadArtifact(filePath)
			results <- uploadResult{index: index, meta: meta, err: err}
		}(i, info.Path)
	}
	wg.Wait()
	close(results)

	metas := make([]artifact.Metadata, len(infos))
	fileResults := make([]adapter.ArtifactFileResult, len(infos))
	var firstErr error
	for result := range results {
		if result.err != nil && firstErr == nil {
			firstErr = result.err
		}
		metas[result.index] = result.meta
		fileResults[result.index] = adapter.ArtifactFileResult{
			Path:   infos[result.index].Path,
			Status: "uploaded",
		}
		if result.err != nil {
			fileResults[result.index].Status = "error"
			fileResults[result.index].Error = controlErrorFromError(result.err, "artifact_upload_failed")
		}
	}
	if firstErr != nil {
		_ = rt.cleanupStagedArtifacts()
		return nil, fileResults, firstErr
	}
	ids := make([]string, 0, len(metas))
	for _, meta := range metas {
		ids = append(ids, meta.ID)
	}
	return ids, fileResults, nil
}

func preflightArtifactFiles(files []string) ([]artifact.LocalFileInfo, []adapter.ArtifactFileResult, error) {
	if len(files) > artifact.MaxFilesPerMessage {
		return nil, nil, artifact.Error{Code: "too_many_artifacts", Message: fmt.Sprintf("at most %d artifacts are allowed", artifact.MaxFilesPerMessage)}
	}
	infos := make([]artifact.LocalFileInfo, 0, len(files))
	results := make([]adapter.ArtifactFileResult, 0, len(files))
	var total int64
	var firstErr error
	for _, path := range files {
		info, err := artifact.ValidateLocalFile(path)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			results = append(results, adapter.ArtifactFileResult{
				Path:   path,
				Status: "error",
				Error:  controlErrorFromError(err, "invalid_artifacts"),
			})
			continue
		}
		total += info.Size
		if total > artifact.MaxTotalBytesPerMessage {
			return nil, nil, artifact.Error{Code: "artifact_batch_too_large", Message: fmt.Sprintf("artifact batch exceeds %d bytes", artifact.MaxTotalBytesPerMessage)}
		}
		infos = append(infos, info)
		results = append(results, adapter.ArtifactFileResult{Path: path, Status: "validated"})
	}
	if firstErr != nil {
		return nil, results, artifact.Error{Code: "invalid_artifacts", Message: "one or more artifacts failed validation; no uploads started"}
	}
	return infos, results, nil
}

func (rt *participantAdapterRuntime) uploadArtifact(path string) (artifact.Metadata, error) {
	info, err := artifact.ValidateLocalFile(path)
	if err != nil {
		return artifact.Metadata{}, err
	}
	meta, err := rt.store.LoadMeta()
	if err != nil {
		return artifact.Metadata{}, err
	}
	if meta.ArtifactEndpoint == "" {
		return artifact.Metadata{}, adapter.ControlError{Code: "artifact_endpoint_unreachable", Message: "artifact endpoint is not available"}
	}
	before, err := os.Stat(path)
	if err != nil {
		return artifact.Metadata{}, err
	}

	bodyReader, bodyWriter := io.Pipe()
	writer := multipart.NewWriter(bodyWriter)
	copyErrCh := make(chan error, 1)
	go func() {
		copyErrCh <- writeArtifactMultipart(bodyWriter, writer, path, info.Name)
	}()

	req, err := http.NewRequest(http.MethodPost, meta.ArtifactEndpoint+"/staged?participant="+url.QueryEscape(rt.cfg.Name), bodyReader)
	if err != nil {
		_ = bodyReader.Close()
		return artifact.Metadata{}, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		_ = bodyReader.CloseWithError(err)
		<-copyErrCh
		return artifact.Metadata{}, err
	}
	copyErr := <-copyErrCh
	defer resp.Body.Close()
	if copyErr != nil {
		return artifact.Metadata{}, copyErr
	}
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return artifact.Metadata{}, controlErrorFromHTTPBody(data, "artifact_upload_failed", "artifact upload failed")
	}
	after, err := os.Stat(path)
	if err != nil {
		return artifact.Metadata{}, err
	}
	if before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return artifact.Metadata{}, adapter.ControlError{Code: "artifact_upload_failed", Message: fmt.Sprintf("artifact changed while uploading: %s", path)}
	}
	var uploaded artifact.Metadata
	if err := json.NewDecoder(resp.Body).Decode(&uploaded); err != nil {
		return artifact.Metadata{}, err
	}
	return uploaded, nil
}

func (rt *participantAdapterRuntime) cleanupStagedArtifacts() error {
	meta, err := rt.store.LoadMeta()
	if err != nil {
		return err
	}
	if meta.ArtifactEndpoint == "" {
		return nil
	}
	req, err := http.NewRequest(http.MethodDelete, meta.ArtifactEndpoint+"/staged?participant="+url.QueryEscape(rt.cfg.Name), nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("artifact cleanup failed: %s", strings.TrimSpace(string(data)))
	}
	return nil
}

func writeArtifactMultipart(pipe *io.PipeWriter, writer *multipart.Writer, path, name string) error {
	file, err := os.Open(path)
	if err != nil {
		_ = pipe.CloseWithError(err)
		return err
	}
	defer file.Close()

	part, err := writer.CreateFormFile("file", name)
	if err != nil {
		_ = pipe.CloseWithError(err)
		return err
	}
	_, copyErr := io.Copy(part, file)
	closeWriterErr := writer.Close()
	if copyErr != nil {
		_ = pipe.CloseWithError(copyErr)
		return copyErr
	}
	if closeWriterErr != nil {
		_ = pipe.CloseWithError(closeWriterErr)
		return closeWriterErr
	}
	return pipe.Close()
}

func (rt *participantAdapterRuntime) fetchArtifacts(ids []string, out string) []adapter.ArtifactFetchResult {
	results := make([]adapter.ArtifactFetchResult, 0, len(ids))
	for _, id := range ids {
		path, err := rt.fetchArtifact(id, out, len(ids) > 1)
		if err != nil {
			results = append(results, adapter.ArtifactFetchResult{ID: id, Status: "error", Error: controlErrorFromError(err, "artifact_unavailable")})
			continue
		}
		results = append(results, adapter.ArtifactFetchResult{ID: id, Status: "downloaded", Path: path})
	}
	return results
}

func (rt *participantAdapterRuntime) fetchArtifact(id, out string, multiple bool) (string, error) {
	meta, err := rt.store.LoadMeta()
	if err != nil {
		return "", err
	}
	if meta.ArtifactEndpoint == "" {
		return "", adapter.ControlError{Code: "artifact_endpoint_unreachable", Message: "artifact endpoint is not available"}
	}
	resp, err := http.Get(meta.ArtifactEndpoint + "/" + id)
	if err != nil {
		return "", adapter.ControlError{Code: "artifact_endpoint_unreachable", Message: err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return "", controlErrorFromHTTPBody(data, "artifact_unavailable", "artifact fetch failed")
	}
	name := artifactNameFromResponse(resp, id)
	target, err := rt.artifactOutputPath(out, name, multiple)
	if err != nil {
		return "", err
	}
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(file, resp.Body); err != nil {
		_ = file.Close()
		_ = os.Remove(target)
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	return target, nil
}

func (rt *participantAdapterRuntime) artifactOutputPath(out, name string, multiple bool) (string, error) {
	if out == "" {
		dir := filepath.Join(filepath.Dir(rt.store.MetaPath), "downloads")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", err
		}
		return collisionSafePath(dir, artifact.SanitizeName(name)), nil
	}
	if info, err := os.Stat(out); err == nil {
		if info.IsDir() {
			return collisionSafePath(out, artifact.SanitizeName(name)), nil
		}
		return "", adapter.ControlError{Code: "output_exists", Message: fmt.Sprintf("output path already exists: %s", out)}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if multiple {
		if err := os.MkdirAll(out, 0o700); err != nil {
			return "", err
		}
		return collisionSafePath(out, artifact.SanitizeName(name)), nil
	}
	parent := filepath.Dir(out)
	if _, err := os.Stat(parent); err != nil {
		return "", adapter.ControlError{Code: "invalid_output_path", Message: fmt.Sprintf("output parent is not available: %s", parent)}
	}
	return out, nil
}

func controlErrorFromError(err error, fallbackCode string) *adapter.ControlError {
	if err == nil {
		return nil
	}
	var controlErr adapter.ControlError
	if errors.As(err, &controlErr) {
		return &adapter.ControlError{Code: controlErr.Code, Message: controlErr.Message}
	}
	var artifactErr artifact.Error
	if errors.As(err, &artifactErr) {
		return &adapter.ControlError{Code: artifactErr.Code, Message: artifactErr.Message}
	}
	return &adapter.ControlError{Code: fallbackCode, Message: err.Error()}
}

func controlErrorFromHTTPBody(data []byte, fallbackCode, fallbackMessage string) adapter.ControlError {
	var payload struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &payload); err == nil && payload.Error.Code != "" {
		return adapter.ControlError{Code: payload.Error.Code, Message: payload.Error.Message}
	}
	message := strings.TrimSpace(string(data))
	if message == "" {
		message = fallbackMessage
	}
	return adapter.ControlError{Code: fallbackCode, Message: message}
}

func artifactNameFromResponse(resp *http.Response, fallback string) string {
	disposition := resp.Header.Get("Content-Disposition")
	if disposition != "" {
		if _, params, err := mime.ParseMediaType(disposition); err == nil && params["filename"] != "" {
			return params["filename"]
		}
	}
	return fallback
}

func collisionSafePath(dir, name string) string {
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return path
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 1; ; i++ {
		candidate := filepath.Join(dir, fmt.Sprintf("%s-%d%s", base, i, ext))
		if _, err := os.Stat(candidate); errors.Is(err, os.ErrNotExist) {
			return candidate
		}
	}
}
