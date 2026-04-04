package web

import (
	"bytes"
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

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(bundle); err != nil {
		return fmt.Errorf("marshal bundle: %w", err)
	}

	// Escape </script> sequences in the JSON to prevent breaking out of the
	// <script> tag. By disabling SetEscapeHTML above, we avoid bloating the
	// export with \u003c for every single '<' character in code blocks.
	// We only need to escape </script> here to be safe from XSS breakout.
	escaped := strings.ReplaceAll(buf.String(), "</script>", `<\/script>`)
	output := strings.Replace(templateHTML, placeholder, escaped, 1)
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
