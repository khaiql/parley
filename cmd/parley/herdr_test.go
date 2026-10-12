package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/khaiql/parley/internal/herdr"
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
