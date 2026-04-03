package web

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

//go:embed template.html
var content embed.FS

var templateHTML string

func init() {
	data, err := content.ReadFile("template.html")
	if err != nil {
		panic("web: embedded template.html not found: " + err.Error())
	}
	templateHTML = string(data)
}

const placeholder = "{{PARLEY_DATA}}"

func Export(dir string, w io.Writer) error {
	room, err := readJSONFile(filepath.Join(dir, "room.json"))
	if err != nil {
		return fmt.Errorf("read room.json: %w", err)
	}

	messages, err := readJSONFile(filepath.Join(dir, "messages.json"))
	if err != nil {
		return fmt.Errorf("read messages.json: %w", err)
	}

	// agents.json is optional
	agents, _ := readJSONFile(filepath.Join(dir, "agents.json"))
	if agents == nil {
		agents = json.RawMessage("[]")
	}

	bundle := struct {
		Room     json.RawMessage `json:"room"`
		Agents   json.RawMessage `json:"agents"`
		Messages json.RawMessage `json:"messages"`
	}{
		Room:     room,
		Agents:   agents,
		Messages: messages,
	}

	bundleJSON, err := json.Marshal(bundle)
	if err != nil {
		return fmt.Errorf("marshal bundle: %w", err)
	}

	output := strings.Replace(templateHTML, placeholder, string(bundleJSON), 1)
	_, err = io.WriteString(w, output)
	return err
}

func readJSONFile(path string) (json.RawMessage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if !json.Valid(data) {
		return nil, fmt.Errorf("%s contains invalid JSON", filepath.Base(path))
	}
	return json.RawMessage(data), nil
}
