package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	assets "github.com/khaiql/parley"
	"github.com/khaiql/parley/internal/herdr"
	"github.com/spf13/cobra"
)

func herdrInstallCmd() *cobra.Command {
	return &cobra.Command{Use: "install", Short: "Link and enable the Herdr plugin using this installed binary", Args: noArgsJSON,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := nativeClient()
			if err != nil {
				return nativeError(cmd, "helper_unavailable", err)
			}
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			root, err := installHerdrPlugin(cmd.Context(), c, filepath.Join(home, ".local", "share", "parley", "herdr", "installed"), version)
			if err != nil {
				return nativeError(cmd, "install_failed", err)
			}
			return writeJSON(cmd, map[string]string{"status": "installed", "plugin_root": root, "version": version, "next": "Controller only: run parley herdr setup in the intended Herdr session. Shortcuts: https://github.com/khaiql/parley#herdr-plugin"})
		}}
}

func installHerdrPlugin(ctx context.Context, c *herdr.Client, base, release string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := c.VerifyVersion(ctx); err != nil {
		return "", err
	}
	run := c.Run
	if run == nil {
		run = herdr.RunProcess
	}
	data, err := run(ctx, c.HerdrBinary, []string{"plugin", "list", "--json"}, nil)
	if err != nil {
		return "", err
	}
	var registry struct {
		Result struct {
			Plugins []struct {
				ID      string `json:"plugin_id"`
				Root    string `json:"plugin_root"`
				Enabled bool   `json:"enabled"`
			} `json:"plugins"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &registry); err != nil {
		return "", err
	}
	installedBinary, err := filepath.EvalSymlinks(c.ParleyBinary)
	if err != nil {
		return "", err
	}
	binary, err := os.ReadFile(installedBinary)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(base, 0700); err != nil {
		return "", err
	}
	base, err = filepath.EvalSymlinks(base)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(append(binary, []byte(release)...))
	root := filepath.Join(base, fmt.Sprintf("%x", digest))
	for _, p := range registry.Result.Plugins {
		if p.ID == herdr.PluginID && p.Enabled && p.Root != root {
			return "", fmt.Errorf("Parley plugin is enabled at %s; finish routed work, run herdr plugin disable parley, wait for old Parley coordinators to exit, then rerun parley herdr install (see https://github.com/khaiql/parley#upgrade-the-plugin)", p.Root)
		}
	}
	stage, err := os.MkdirTemp(base, ".install-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(stage)
	files := map[string][]byte{"bin/parley": binary}
	for _, name := range []string{"herdr-plugin.toml", "skills/parley/SKILL.md", "skills/parley/scripts/ensure-parley"} {
		content, err := assets.PluginFiles.ReadFile(name)
		if err != nil {
			return "", err
		}
		if name == "herdr-plugin.toml" {
			lines := strings.Split(string(content), "\n")
			for i, line := range lines {
				if strings.HasPrefix(line, "version = ") {
					lines[i] = fmt.Sprintf("version = %q", release)
				}
			}
			content = []byte(strings.Join(lines, "\n"))
		}
		files[name] = content
	}
	for name, content := range files {
		path := filepath.Join(stage, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return "", err
		}
		mode := os.FileMode(0600)
		if name == "bin/parley" || name == "skills/parley/scripts/ensure-parley" {
			mode = 0700
		}
		// Keep the installed inode alive through Homebrew cleanup; copy only across filesystems.
		if name == "bin/parley" && os.Link(installedBinary, path) == nil {
			continue
		}
		if err := os.WriteFile(path, content, mode); err != nil {
			return "", err
		}
	}
	if info, err := os.Lstat(root); err == nil {
		if !info.IsDir() {
			return "", fmt.Errorf("refusing to replace plugin path: %s", root)
		}
		for name, want := range files {
			path := filepath.Join(root, name)
			info, err := os.Lstat(path)
			if err != nil || !info.Mode().IsRegular() {
				return "", fmt.Errorf("invalid installed plugin file: %s", path)
			}
			got, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(got, want) {
				return "", fmt.Errorf("installed plugin differs; refusing to overwrite %s", path)
			}
			if (name == "bin/parley" || name == "skills/parley/scripts/ensure-parley") && info.Mode()&0111 == 0 {
				return "", fmt.Errorf("plugin file is not executable: %s", path)
			}
		}
	} else if !os.IsNotExist(err) {
		return "", err
	} else if err := os.Rename(stage, root); err != nil {
		return "", err
	}
	if _, err := run(ctx, c.HerdrBinary, []string{"plugin", "link", root, "--enabled"}, nil); err != nil {
		return "", err
	}
	return root, nil
}
