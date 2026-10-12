package main

import (
	"bytes"
	"context"
	"github.com/khaiql/parley/internal/herdr"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHerdrRPCRejectBeforeProcess(t *testing.T) {
	calls := 0
	c := &herdr.Client{Run: func(context.Context, string, []string, []byte) ([]byte, error) { calls++; return nil, nil }}
	for _, s := range []string{`{"operation":"probe","session":"agents","path":"/tmp"}`, `{"operation":"join","session":"agents"}`} {
		if _, e := runHerdrRPC(context.Background(), strings.NewReader(s), &bytes.Buffer{}, c); e == nil {
			t.Fatal(s)
		}
	}
	if calls != 0 {
		t.Fatal(calls)
	}
}
func TestHerdrRPCRejectedHelperChild(t *testing.T) {
	if os.Getenv("PARLEY_RPC_REJECTION_CHILD") != "1" {
		return
	}
	cmd := newRootCmd()
	cmd.SetArgs([]string{"herdr", "rpc"})
	cmd.SetIn(os.Stdin)
	cmd.SetErr(os.Stderr)
	if e := cmd.Execute(); e != nil {
		os.Exit(1)
	}
	os.Exit(0)
}
func TestHerdrRPCEventStartupRejection(t *testing.T) {
	dir := t.TempDir()
	count := filepath.Join(dir, "calls")
	testBinary := os.Args[0]
	script := "#!/bin/sh\nprintf x >> '" + count + "'\nexec '" + testBinary + "' -test.run=^TestHerdrRPCRejectedHelperChild$\n"
	if e := os.WriteFile(filepath.Join(dir, "ssh"), []byte(script), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dir, "herdr"), []byte("#!/bin/sh\nprintf '%s\\n' '{\"sessions\":[]}'\n"), 0700); e != nil {
		t.Fatal(e)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	t.Setenv("PARLEY_RPC_REJECTION_CHILD", "1")
	key, _ := herdr.SessionKey("/tmp/a.sock")
	peer := herdr.Peer{Machine: herdr.Machine{ID: "peer", Target: "host", Session: "agents"}, Binary: "/opt/parley", Config: herdr.SessionConfig{SessionKey: key, ControllerID: "local"}}
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	reads := 0
	c := herdr.Client{}
	e := c.WatchRequests(ctx, peer, func(context.Context, herdr.Peer) error { reads++; return nil })
	calls, _ := os.ReadFile(count)
	if e == nil || !strings.Contains(e.Error(), "saved session unavailable") || reads != 0 || string(calls) != "x" {
		t.Fatal(e, reads, string(calls))
	}
}
