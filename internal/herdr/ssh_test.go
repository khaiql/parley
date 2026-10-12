package herdr

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestQuietStartupMaster(t *testing.T) {
	dir := t.TempDir()
	if e := os.WriteFile(filepath.Join(dir, "ssh"), []byte("#!/bin/sh\ncase \"$*\" in *'-O check'*) exit 0;; esac\nexec sleep 60\n"), 0700); e != nil {
		t.Fatal(e)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	m, e := StartMaster(context.Background(), Peer{Machine: Machine{ID: "peer", Target: "host", Session: "agents"}})
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	args := strings.Join(m.Command.Args, " ")
	for _, flag := range []string{"-M", "-S", "-N", "ControlPersist=no", "ClearAllForwardings=yes"} {
		if !strings.Contains(args, flag) {
			t.Fatal(args)
		}
	}
	if strings.Contains(args, "-L") || strings.Contains(args, "-R") {
		t.Fatal(args)
	}
	m.Close()
	if m.Command.ProcessState == nil {
		t.Fatal("master not reaped")
	}
	if _, e = os.Stat(filepath.Dir(m.ControlPath)); !os.IsNotExist(e) {
		t.Fatal("control root survived", e)
	}
}
