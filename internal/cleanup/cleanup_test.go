package cleanup

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ArfaMujahid/scraper/internal/job"
	"github.com/ArfaMujahid/scraper/internal/registry"
)

// slogDiscard returns a logger that drops all output, keeping test logs quiet.
func slogDiscard() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// writeFile writes a file aged by `age` into ownerDir (created if needed).
func writeFile(t *testing.T, ownerDir, name string, age time.Duration) string {
	t.Helper()
	if err := os.MkdirAll(ownerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(ownerDir, name)
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().Add(-age)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	return path
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestSweepDeletesOldKeepsFresh(t *testing.T) {
	root := t.TempDir()
	alice := filepath.Join(root, "alice")
	oldJSONL := writeFile(t, alice, "old1_20200101T000000Z.jsonl", 2*time.Hour)
	oldCSV := writeFile(t, alice, "old2_20200101T000000Z.csv", 2*time.Hour)
	fresh := writeFile(t, alice, "new1_20990101T000000Z.jsonl", 0)
	other := writeFile(t, alice, "old3_20200101T000000Z.txt", 2*time.Hour) // wrong ext

	jn := New(root, time.Hour, time.Hour, registry.New(), slogDiscard())
	jn.sweep()

	if exists(oldJSONL) || exists(oldCSV) {
		t.Error("old .jsonl/.csv should be deleted")
	}
	if !exists(fresh) {
		t.Error("fresh file should be kept")
	}
	if !exists(other) {
		t.Error("non-output extension must never be touched")
	}
}

func TestSweepProtectsActiveJob(t *testing.T) {
	root := t.TempDir()
	alice := filepath.Join(root, "alice")
	path := writeFile(t, alice, "run1_20200101T000000Z.jsonl", 2*time.Hour)

	reg := registry.New()
	reg.Add(&job.Job{ID: "run1", Owner: "alice", Status: job.StatusQueued})
	reg.SetRunning("alice", "run1")

	jn := New(root, time.Hour, time.Hour, reg, slogDiscard())
	jn.sweep()

	if !exists(path) {
		t.Error("a running job's file must never be deleted, even when old")
	}
}

func TestSweepDeletesAndEvictsCompletedJob(t *testing.T) {
	root := t.TempDir()
	alice := filepath.Join(root, "alice")
	path := writeFile(t, alice, "done1_20200101T000000Z.jsonl", 2*time.Hour)

	reg := registry.New()
	reg.Add(&job.Job{ID: "done1", Owner: "alice", Status: job.StatusDone})

	jn := New(root, time.Hour, time.Hour, reg, slogDiscard())
	jn.sweep()

	if exists(path) {
		t.Error("a completed old job's file should be deleted")
	}
	if _, ok := reg.Get("alice", "done1"); ok {
		t.Error("registry entry for the deleted job should be evicted")
	}
}

func TestSweepRemovesEmptyOwnerDir(t *testing.T) {
	root := t.TempDir()
	emptyAfter := filepath.Join(root, "alice")
	writeFile(t, emptyAfter, "old1_x.jsonl", 2*time.Hour)
	kept := filepath.Join(root, "bob")
	writeFile(t, kept, "new1_x.jsonl", 0)

	jn := New(root, time.Hour, time.Hour, registry.New(), slogDiscard())
	jn.sweep()

	if exists(emptyAfter) {
		t.Error("owner dir should be removed once empty")
	}
	if !exists(kept) {
		t.Error("owner dir with fresh files should be kept")
	}
}

func TestSweepMissingDirIsNoError(t *testing.T) {
	jn := New(filepath.Join(t.TempDir(), "does-not-exist"), time.Hour, time.Hour, registry.New(), slogDiscard())
	jn.sweep() // must not panic
}

func TestRunStopsOnContextCancel(t *testing.T) {
	jn := New(t.TempDir(), time.Hour, 10*time.Millisecond, registry.New(), slogDiscard())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		jn.Run(ctx)
		close(done)
	}()
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop after context cancellation")
	}
}
