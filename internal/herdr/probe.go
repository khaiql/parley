package herdr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/khaiql/parley/internal/descriptor"
)

func artifactEndpoint(raw, roomID string) (*url.URL, error) {
	if err := safeID(roomID); err != nil {
		return nil, err
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") || u.RawPath != "" || u.Path != "/rooms/"+roomID+"/artifacts" {
		return nil, fmt.Errorf("invalid artifact endpoint")
	}
	return u, nil
}
func validateEndpoints(endpoints Endpoints, roomID string) (descriptor.Descriptor, *url.URL, error) {
	d, err := descriptor.Parse(endpoints.Descriptor)
	if err != nil || d.RoomID != roomID || d.Host != "127.0.0.1" {
		return d, nil, fmt.Errorf("invalid protocol endpoint")
	}
	u, err := artifactEndpoint(endpoints.ArtifactURL, roomID)
	return d, u, err
}

// ProbeArtifact checks existing rejection paths without reading or changing artifacts.
func ProbeArtifact(ctx context.Context, rawEndpoint, roomID string) error {
	u, err := artifactEndpoint(rawEndpoint, roomID)
	if err != nil {
		return err
	}
	negative := "parley-route-probe"
	if negative == roomID {
		negative += "-other"
	}
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	for _, probe := range []struct {
		room          string
		status        int
		code, message string
	}{{roomID, 400, "bad_request", "participant query parameter is required"}, {negative, 404, "artifact_unavailable", "artifact route is not available"}} {
		endpoint := *u
		endpoint.Path = "/rooms/" + probe.room + "/artifacts/staged"
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 65537))
		_ = resp.Body.Close()
		media, _, mediaErr := mime.ParseMediaType(resp.Header.Get("Content-Type"))
		if readErr != nil || len(body) > 65536 || resp.StatusCode != probe.status || mediaErr != nil || media != "application/json" {
			return fmt.Errorf("artifact route probe rejected")
		}
		var got struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&got) != nil || decoder.Decode(new(any)) != io.EOF || got.Error.Code != probe.code || got.Error.Message != probe.message {
			return fmt.Errorf("artifact route probe rejected")
		}
	}
	return nil
}
func probeRoute(ctx context.Context, endpoints Endpoints, roomID string) error {
	d, _, err := validateEndpoints(endpoints, roomID)
	if err != nil {
		return err
	}
	conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", d.Addr())
	if err != nil {
		return fmt.Errorf("protocol route probe: %w", err)
	}
	_ = conn.Close()
	return ProbeArtifact(ctx, endpoints.ArtifactURL, roomID)
}
