package herdr

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func target() AgentTarget {
	return AgentTarget{MachineID: "self", HerdrSocket: "/private/herdr.sock", TerminalID: "term_a", Kind: "codex", NativeSession: "a"}
}
func request(id string) Request {
	return Request{ID: id, Op: "join", RoomID: "room-a", Target: target()}
}
func TestCorruptStateIsNotReset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	os.WriteFile(path, []byte("{"), 0600)
	s := NewStore(dir)
	if _, err := s.Read(); err == nil {
		t.Fatal("corrupt state accepted")
	}
	if err := s.Update(func(*State) error { return nil }); err == nil {
		t.Fatal("corrupt state overwritten")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "{" {
		t.Fatal("original state destroyed")
	}
}
func TestTwoPendingRequestsSurviveOneMarker(t *testing.T) {
	s := NewStore(t.TempDir())
	for _, id := range []string{"request-a", "request-b"} {
		if err := s.Enqueue(request(id)); err != nil {
			t.Fatal(err)
		}
	}
	p, err := s.Pending()
	if err != nil || len(p) != 2 {
		t.Fatalf("pending=%v err=%v", p, err)
	}
}
func TestSessionNamespaceIsolation(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := filepath.Join(root, "alpha.sock")
	b := filepath.Join(root, "beta.sock")
	os.WriteFile(a, nil, 0600)
	alias := filepath.Join(root, "alias.sock")
	os.Symlink(a, alias)
	canonical, _ := filepath.EvalSymlinks(alias)
	ka, _ := SessionKey(a)
	kc, _ := SessionKey(canonical)
	if ka != kc {
		t.Fatal("canonical alias differs")
	}
	for _, bad := range []string{"relative.sock", "/a/../b.sock"} {
		if _, err := SessionKey(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	ca, sa, err := SessionDirs(filepath.Join(root, "config"), filepath.Join(root, "state"), a)
	if err != nil {
		t.Fatal(err)
	}
	cb, sb, _ := SessionDirs(filepath.Join(root, "config"), filepath.Join(root, "state"), b)
	if ca == cb || sa == sb {
		t.Fatal("sessions collide")
	}
	os.MkdirAll(ca, 0700)
	os.MkdirAll(cb, 0700)
	os.WriteFile(filepath.Join(ca, "config.json"), []byte(`{"role":"controller"}`), 0600)
	if _, err := os.ReadFile(filepath.Join(cb, "config.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("sibling config visible")
	}
	s := NewStore(sa)
	if err := s.Enqueue(request("one")); err != nil {
		t.Fatal(err)
	}
	if err := s.Update(func(v *State) error { v.ControllerID = "owner-a"; v.Rooms["a"] = Room{ID: "a"}; return nil }); err != nil {
		t.Fatal(err)
	}
	other := NewStore(sb)
	p, err := other.Pending()
	if err != nil || len(p) != 0 {
		t.Fatal(p, err)
	}
	state, err := other.Read()
	if err != nil || state.ControllerID != "" || len(state.Rooms) != 0 {
		t.Fatal(state, err)
	}
	state, err = NewStore(sa).Read()
	if err != nil || state.ControllerID != "owner-a" || len(state.Rooms) != 1 {
		t.Fatal(state, err)
	}
}
func TestHostBindingKey(t *testing.T) {
	v := target()
	k := HostBindingKey("r", v)
	if k != "623e6f2b5845c41fdd3e5a64c3eeedd457f3fb354aa7159fd7a5920c51edcfaf" {
		t.Fatal("binding tuple/hash mismatch", k)
	}
	v.MachineID = "profile-id"
	if HostBindingKey("r", v) != k {
		t.Fatal("machine id changes host identity")
	}
	for _, change := range []func(*AgentTarget){func(v *AgentTarget) { v.HerdrSocket = "/private/other.sock" }, func(v *AgentTarget) { v.TerminalID = "other" }, func(v *AgentTarget) { v.Kind = "claude" }, func(v *AgentTarget) { v.NativeSession = "other" }} {
		v := target()
		change(&v)
		if HostBindingKey("r", v) == k {
			t.Fatal("distinct identity collides")
		}
	}
	if HostBindingKey("other", target()) == k {
		t.Fatal("rooms collide")
	}
}
func TestClaimDurableImmutable(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	r := request("one")
	if err := s.Enqueue(r); err != nil {
		t.Fatal(err)
	}
	if err := s.Enqueue(r); err != nil {
		t.Fatal(err)
	}
	r.RoomID = "room-b"
	if err := s.Enqueue(r); err == nil || !strings.Contains(err.Error(), "request_conflict") {
		t.Fatal("changed body accepted")
	}
	o, err := s.Claim("one", fixtureDigest(), "owner")
	if err != nil || o.Phase != "claimed" {
		t.Fatal(o, err)
	}
	again, err := NewStore(dir).Claim("one", fixtureDigest(), "owner")
	if err != nil || again != o {
		t.Fatal(again, err)
	}
	for _, args := range [][2]string{{strings.Repeat("f", 64), "owner"}, {fixtureDigest(), "other"}} {
		if _, err := s.Claim("one", args[0], args[1]); err == nil {
			t.Fatal("conflicting claim accepted")
		}
	}
	o.Phase = "intent"
	if err := s.Complete("one", o); err != nil {
		t.Fatal(err)
	}
	again, err = NewStore(dir).Claim("one", fixtureDigest(), "owner")
	if err != nil || again.Phase != "intent" {
		t.Fatal(again, err)
	}
	p, err := s.Pending()
	if err != nil || len(p) != 1 {
		t.Fatal(p, err)
	}
	o.Phase = "complete"
	o.Response = Response{Status: "ok"}
	if err := s.Complete("one", o); err != nil {
		t.Fatal(err)
	}
	p, err = s.Pending()
	if err != nil || len(p) != 0 {
		t.Fatal(p, err)
	}
	o.Response.Status = "changed"
	if err := s.Complete("one", o); err == nil {
		t.Fatal("complete outcome changed")
	}
}
func TestClaimConcurrentControllers(t *testing.T) {
	s := NewStore(t.TempDir())
	s.Enqueue(request("one"))
	var wg sync.WaitGroup
	results := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Go(func() { _, err := s.Claim("one", fixtureDigest(), string(rune('a'+i))); results <- err })
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("%d owners", success)
	}
}
func TestStoreConcurrentUpdates(t *testing.T) {
	s := NewStore(t.TempDir())
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Go(func() {
			err := s.Update(func(v *State) error { id := string(rune('A' + i)); v.Rooms[id] = Room{ID: id}; return nil })
			if err != nil {
				t.Error(err)
			}
			if _, err := s.Read(); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	v, err := s.Read()
	if err != nil || len(v.Rooms) != 30 {
		t.Fatal(v, err)
	}
}
func TestStoreRejectsSymlinksAndCorruptOutcomes(t *testing.T) {
	for _, name := range []string{"state.json", "requests/one.json", "outcomes/one.json", "locks"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			s := NewStore(dir)
			s.Enqueue(request("one"))
			path := filepath.Join(dir, name)
			os.Remove(path)
			outside := filepath.Join(t.TempDir(), "victim")
			os.WriteFile(outside, []byte("{}"), 0600)
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, path); err != nil {
				t.Fatal(err)
			}
			var err error
			switch name {
			case "state.json":
				_, err = s.Read()
			case "requests/one.json":
				err = s.Enqueue(request("one"))
			case "outcomes/one.json":
				_, err = s.Claim("one", fixtureDigest(), "owner")
			case "locks":
				err = s.WithBinding(context.Background(), "join:key", func() error { return nil })
			}
			if err == nil {
				t.Fatal("symlink accepted")
			}
			data, _ := os.ReadFile(outside)
			if string(data) != "{}" {
				t.Fatal("victim changed")
			}
		})
	}
	dir := t.TempDir()
	s := NewStore(dir)
	s.Enqueue(request("one"))
	path := filepath.Join(dir, "outcomes", "one.json")
	os.MkdirAll(filepath.Dir(path), 0700)
	os.WriteFile(path, []byte("{"), 0600)
	if _, err := s.Claim("one", fixtureDigest(), "owner"); err == nil {
		t.Fatal("corrupt outcome accepted")
	}
	if _, err := s.Pending(); err == nil {
		t.Fatal("corrupt outcome hidden")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "{" {
		t.Fatal("corrupt outcome destroyed")
	}
}
func TestStorePrivatePermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "store")
	s := NewStore(dir)
	s.Enqueue(request("one"))
	s.Claim("one", fixtureDigest(), "owner")
	s.Update(func(*State) error { return nil })
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			t.Fatal(err)
		}
		want := os.FileMode(0600)
		if info.IsDir() {
			want = 0700
		}
		if info.Mode().Perm() != want {
			t.Errorf("%s mode=%o", path, info.Mode().Perm())
		}
		return nil
	})
}
func TestBindingLockAcrossStores(t *testing.T) {
	if dir := os.Getenv("HERDR_LOCK_TEST_DIR"); dir != "" {
		s := NewStore(dir)
		if os.Getenv("HERDR_LOCK_TEST_RELEASED") == "yes" {
			if err := s.WithBinding(context.Background(), "join:key", func() error {
				state, err := s.Read()
				if err != nil {
					return err
				}
				if state.Bindings["key"].JoinRequestID != "one" {
					t.Fatal("persisted intent lost after release")
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
		defer cancel()
		err := s.WithBinding(ctx, "join:key", func() error { t.Fatal("callback entered held lock"); return nil })
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
		return
	}
	dir := t.TempDir()
	s := NewStore(dir)
	err := s.WithBinding(context.Background(), "route:key", func() error {
		return s.WithBinding(context.Background(), "join:key", func() error {
			if err := s.Update(func(v *State) error {
				v.Bindings["key"] = Binding{JoinPhase: "intent", JoinRequestID: "one"}
				return nil
			}); err != nil {
				return err
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestBindingLockAcrossStores$", "-test.count=1")
			cmd.Env = append(os.Environ(), "HERDR_LOCK_TEST_DIR="+dir)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("child %s %v", out, err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
			defer cancel()
			if err := NewStore(dir).WithBinding(ctx, "join:key", func() error { t.Fatal("second store entered"); return nil }); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal(err)
			}
			return nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := NewStore(dir).WithBinding(context.Background(), "join:key", func() error {
		v, err := s.Read()
		if err != nil {
			return err
		}
		if v.Bindings["key"].JoinRequestID != "one" {
			t.Fatal("intent lost")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestBindingLockAcrossStores$", "-test.count=1")
	cmd.Env = append(os.Environ(), "HERDR_LOCK_TEST_DIR="+dir, "HERDR_LOCK_TEST_RELEASED=yes")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("released child %s %v", out, err)
	}
}
func TestStoreRejectsUnsafeIDs(t *testing.T) {
	for _, id := range []string{"../bad", "a/b", "a\\b", "", strings.Repeat("a", 129)} {
		s := NewStore(t.TempDir())
		if err := s.Enqueue(request(id)); err == nil {
			t.Errorf("accepted %q", id)
		}
	}
}

func fixtureDigest() string {
	b := []byte(`{"id":"one","op":"join","room_id":"room-a","target":{"machine_id":"self","herdr_socket":"/private/herdr.sock","workspace_id":"","pane_id":"","terminal_id":"term_a","kind":"codex","native_session":"a"}}`)
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func TestClaimNormalizedOriginDigest(t *testing.T) {
	s := NewStore(t.TempDir())
	raw := request("one")
	if err := s.Enqueue(raw); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	normalized, err := DecodeRequest(bytes.NewReader(body), peer())
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := s.Claim(raw.ID, RequestDigest(normalized), "owner")
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Digest == fixtureDigest() {
		t.Fatal("raw origin hashed")
	}
	pending, err := s.Pending()
	if err != nil || len(pending) != 1 {
		t.Fatal(pending, err)
	}
	again, err := s.Claim(raw.ID, RequestDigest(normalized), "owner")
	if err != nil || again != outcome {
		t.Fatal(again, err)
	}
}
func TestStoreRejectsNonRegularRecord(t *testing.T) {
	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, "state.json"), 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := NewStore(dir).Read(); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("FIFO accepted")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("read blocked on FIFO")
	}
}

func TestStoreFailedUpdatePreservesState(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := s.Update(func(v *State) error { v.ControllerID = "original"; return nil }); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("abort")
	if err := s.Update(func(v *State) error { v.ControllerID = "changed"; return failure }); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	v, err := s.Read()
	if err != nil || v.ControllerID != "original" {
		t.Fatal(v, err)
	}
}
func TestBindingLockRejectsSymlinkFile(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if err := s.WithBinding(context.Background(), "join:key", func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "locks"))
	if err != nil || len(entries) != 1 {
		t.Fatal(entries, err)
	}
	path := filepath.Join(dir, "locks", entries[0].Name())
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("safe"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, path); err != nil {
		t.Fatal(err)
	}
	if err := s.WithBinding(context.Background(), "join:key", func() error { t.Fatal("symlink lock callback entered"); return nil }); err == nil {
		t.Fatal("symlink lock accepted")
	}
}

func TestSessionKeyAndRequestDigest(t *testing.T) {
	key, err := SessionKey("/private/herdr.sock")
	if err != nil || key != "ca321d5b88bfd019922330d0e61b1c6c697d04b5eda47fddd6af6cbf5ed87d32" {
		t.Fatal(key, err)
	}
	if RequestDigest(request("one")) != fixtureDigest() {
		t.Fatal("request hash mismatch")
	}
}

func TestStoreReadHydratesOutcomesAndRejectsIndexMutation(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if err := s.Enqueue(request("one")); err != nil {
		t.Fatal(err)
	}
	o, err := s.Claim("one", fixtureDigest(), "owner")
	if err != nil {
		t.Fatal(err)
	}
	state, err := NewStore(dir).Read()
	if err != nil || state.Requests["one"].Phase != "claimed" {
		t.Fatal(state, err)
	}
	o.Phase = "intent"
	o.Response = Response{Status: "pending", Room: &Room{ID: "room-a"}}
	if err := s.Complete("one", o); err != nil {
		t.Fatal(err)
	}
	state, err = NewStore(dir).Read()
	if err != nil || state.Requests["one"].Phase != "intent" {
		t.Fatal(state, err)
	}
	if err := s.Update(func(v *State) error { delete(v.Requests, "one"); v.ControllerID = "should-not-save"; return nil }); err == nil {
		t.Fatal("derived outcome deletion accepted")
	}
	if err := s.Update(func(v *State) error { v.Requests["one"].Response.Room.ID = "changed"; return nil }); err == nil {
		t.Fatal("nested outcome mutation accepted")
	}
	if err := s.Update(func(v *State) error { v.ControllerID = "owner"; return nil }); err != nil {
		t.Fatal(err)
	}
	var persisted State
	if err := readJSON(filepath.Join(dir, "state.json"), &persisted); err != nil {
		t.Fatal(err)
	}
	if len(persisted.Requests) != 0 {
		t.Fatal("derived outcomes duplicated on disk")
	}
	o.Phase = "complete"
	o.Response = Response{Status: "ok"}
	if err := s.Complete("one", o); err != nil {
		t.Fatal(err)
	}
	state, err = NewStore(dir).Read()
	if err != nil || state.Requests["one"].Phase != "complete" || state.ControllerID != "owner" {
		t.Fatal(state, err)
	}
	path := filepath.Join(dir, "outcomes", "one.json")
	if err := os.WriteFile(path, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Read(); err == nil {
		t.Fatal("corrupt outcome hidden by Read")
	}
	if err := s.Update(func(*State) error { return nil }); err == nil {
		t.Fatal("Update accepted corrupt outcome")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "{" {
		t.Fatal("corrupt outcome destroyed")
	}
}
