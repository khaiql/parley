package herdr

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRunProcessChild(t *testing.T) {
	if len(os.Args) < 2 || os.Args[len(os.Args)-1] != "herdr-child" {
		return
	}
	b, _ := io.ReadAll(os.Stdin)
	switch string(b) {
	case "helper-error":
		io.WriteString(os.Stdout, `{"status":"error","error":{"code":"herdr_failed","message":"controller_conflict: remote session owned by another controller"}}`)
		io.WriteString(os.Stderr, "helper diagnostic")
		os.Exit(1)
	case "overflow":
		os.Stdout.Write([]byte(strings.Repeat("x", outputLimit+1)))
		time.Sleep(time.Minute)
	case "sleep":
		time.Sleep(time.Minute)
	default:
		os.Stdout.Write(b)
	}
	os.Exit(0)
}
func TestRunProcessBounded(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"overflow", "sleep"} {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		started := time.Now()
		_, e := RunProcess(ctx, os.Args[0], []string{"-test.run=^TestRunProcessChild$", "herdr-child"}, []byte(mode))
		cancel()
		if e == nil {
			t.Fatal(mode)
		}
		if mode == "overflow" && !strings.Contains(e.Error(), "exceeds limit") {
			t.Fatal(e)
		}
		if time.Since(started) > 5*time.Second {
			t.Fatal("child not reaped promptly")
		}
	}
}

func TestRunProcessPreservesHelperError(t *testing.T) {
	_, err := RunProcess(context.Background(), os.Args[0], []string{"-test.run=^TestRunProcessChild$", "herdr-child"}, []byte("helper-error"))
	if err == nil {
		t.Fatal("helper failure lost")
	}
	for _, want := range []string{"herdr_failed", "controller_conflict: remote session owned by another controller", "helper diagnostic", "exit status 1"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("missing %q: %v", want, err)
		}
	}
}
