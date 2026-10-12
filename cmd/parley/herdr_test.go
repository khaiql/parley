package main

import (
	"bytes"
	"context"
	"github.com/khaiql/parley/internal/herdr"
	"strings"
	"testing"
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
