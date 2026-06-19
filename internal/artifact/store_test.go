package artifact

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestArtifactSanitizeNameKeepsBasenameOnly(t *testing.T) {
	if got := SanitizeName("../logs/trace.json"); got != "trace.json" {
		t.Fatalf("SanitizeName = %q, want trace.json", got)
	}
}

func TestArtifactValidateRejectsDirectoryAndSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(target, []byte("data"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if _, err := ValidateLocalFile(dir); !IsCode(err, "artifact_must_be_file") {
		t.Fatalf("directory validation err = %v, want artifact_must_be_file", err)
	}
	if _, err := ValidateLocalFile(link); !IsCode(err, "artifact_must_be_regular_file") {
		t.Fatalf("symlink validation err = %v, want artifact_must_be_regular_file", err)
	}
}

func TestArtifactValidateAllowsEmptyRegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.txt")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("write empty file: %v", err)
	}

	info, err := ValidateLocalFile(path)
	if err != nil {
		t.Fatalf("ValidateLocalFile: %v", err)
	}
	if info.Size != 0 || info.Name != "empty.txt" {
		t.Fatalf("info = %#v, want empty.txt size 0", info)
	}
}

func TestArtifactStoreStagesCommitsAndOpensByID(t *testing.T) {
	store := NewStore(t.TempDir())
	staged, err := store.Stage("alice", "trace.json", []byte("hello"))
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}

	committed, err := store.Commit("alice", []string{staged.ID})
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if len(committed) != 1 || committed[0].ID != staged.ID || committed[0].SHA256 == "" {
		t.Fatalf("committed = %#v, want staged metadata", committed)
	}

	rc, meta, err := store.Open(committed[0].ID)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(data) != "hello" || meta.Name != "trace.json" {
		t.Fatalf("opened data/meta = %q %#v, want hello trace.json", data, meta)
	}
}

func TestArtifactStoreStageReaderRejectsOversizedStreamAtLimit(t *testing.T) {
	store := NewStore(t.TempDir())
	reader := &countingByteReader{remaining: MaxFileBytes + 1024*1024}

	meta, err := store.StageReader("alice", "huge.bin", -1, reader)
	if !IsCode(err, "artifact_too_large") {
		t.Fatalf("StageReader err = %v, want artifact_too_large", err)
	}
	if meta.ID != "" {
		t.Fatalf("metadata = %#v, want no staged metadata", meta)
	}
	if reader.read > MaxFileBytes+1 {
		t.Fatalf("StageReader read %d bytes, want at most MaxFileBytes+1 (%d)", reader.read, MaxFileBytes+1)
	}
	if _, err := store.Commit("alice", []string{"huge.bin"}); !IsCode(err, "artifact_unavailable") {
		t.Fatalf("Commit oversized artifact err = %v, want artifact_unavailable", err)
	}
}

func TestArtifactStoreCleanupStagedForParticipant(t *testing.T) {
	store := NewStore(t.TempDir())
	alice, err := store.Stage("alice", "alice.txt", []byte("alice"))
	if err != nil {
		t.Fatalf("Stage alice: %v", err)
	}
	bob, err := store.Stage("bob", "bob.txt", []byte("bob"))
	if err != nil {
		t.Fatalf("Stage bob: %v", err)
	}

	if err := store.CleanupStaged("alice"); err != nil {
		t.Fatalf("CleanupStaged: %v", err)
	}
	if _, err := store.Commit("alice", []string{alice.ID}); !IsCode(err, "artifact_unavailable") {
		t.Fatalf("alice commit err = %v, want artifact_unavailable", err)
	}
	if _, err := store.Commit("bob", []string{bob.ID}); err != nil {
		t.Fatalf("bob staged artifact should remain: %v", err)
	}
}

type countingByteReader struct {
	remaining int64
	read      int64
}

func (r *countingByteReader) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	n := len(p)
	if int64(n) > r.remaining {
		n = int(r.remaining)
	}
	for i := 0; i < n; i++ {
		p[i] = 'x'
	}
	r.remaining -= int64(n)
	r.read += int64(n)
	return n, nil
}
