package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/khaiql/parley/internal/herdr"
)

func TestInstallHerdrReusesBinaryAndSupportsUpgrade(t *testing.T) {
	base := filepath.Join(t.TempDir(), "linked-install-root")
	if err := os.Symlink(t.TempDir(), base); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "parley")
	if err := os.WriteFile(binary, []byte("installed CLI"), 0755); err != nil {
		t.Fatal(err)
	}
	var linked string
	enabled := false
	invoked := filepath.Join(t.TempDir(), "homebrew-parley")
	if err := os.Symlink(binary, invoked); err != nil {
		t.Fatal(err)
	}
	c := &herdr.Client{HerdrBinary: "herdr", ParleyBinary: invoked}
	c.Run = func(_ context.Context, _ string, args []string, _ []byte) ([]byte, error) {
		switch strings.Join(args, " ") {
		case "--version":
			return []byte("herdr 0.9.3"), nil
		case "plugin list --json":
			return json.Marshal(map[string]any{"result": map[string]any{"plugins": []map[string]any{{"plugin_id": "parley", "plugin_root": linked, "enabled": enabled}}}})
		}
		if len(args) != 4 || args[0] != "plugin" || args[1] != "link" || args[3] != "--enabled" {
			t.Fatalf("unexpected command: %v", args)
		}
		var err error
		linked, err = filepath.EvalSymlinks(args[2])
		if err != nil {
			t.Fatal(err)
		}
		enabled = true
		return []byte(`{"result":{"type":"plugin_linked"}}`), nil
	}
	root, err := installHerdrPlugin(context.Background(), c, base, "0.4.0")
	if err != nil {
		t.Fatal(err)
	}
	first, err := os.Stat(filepath.Join(root, "bin/parley"))
	if err != nil {
		t.Fatal(err)
	}
	original, _ := os.Stat(binary)
	if !os.SameFile(first, original) {
		t.Fatal("same filesystem should reuse existing binary")
	}
	for _, name := range []string{"herdr-plugin.toml", "skills/parley/SKILL.md", "skills/parley/scripts/ensure-parley"} {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || len(data) == 0 {
			t.Fatalf("missing resource %s: %v", name, err)
		}
		if name == "herdr-plugin.toml" && !strings.Contains(string(data), `version = "0.4.0"`) {
			t.Fatal(string(data))
		}
	}
	again, err := installHerdrPlugin(context.Background(), c, base, "0.4.0")
	if err != nil || again != root {
		t.Fatal(again, err)
	}
	// Package managers replace executables rather than mutating a running inode.
	replacement := filepath.Join(filepath.Dir(binary), "new")
	if err := os.WriteFile(replacement, []byte("upgraded CLI"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, binary); err != nil {
		t.Fatal(err)
	}
	if _, err := installHerdrPlugin(context.Background(), c, base, "0.5.0"); err == nil || !strings.Contains(err.Error(), "disable") {
		t.Fatalf("enabled upgrade must explain safe recovery: %v", err)
	}
	if linked != root {
		t.Fatal("failed upgrade changed registration")
	}
	enabled = false
	upgraded, err := installHerdrPlugin(context.Background(), c, base, "0.5.0")
	if err != nil || upgraded == root || linked != upgraded || !enabled {
		t.Fatal(upgraded, err)
	}
	old, err := os.ReadFile(filepath.Join(root, "bin/parley"))
	if err != nil || string(old) != "installed CLI" {
		t.Fatal("upgrade damaged old release", err)
	}
}
