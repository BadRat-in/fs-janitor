package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/BadRat-in/fs-janitor/internal/engine"
	"github.com/BadRat-in/fs-janitor/internal/job"
)

func TestCountChildren(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"a.txt", "b.txt", ".hidden", ".DS_Store"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	a := &App{}
	if got := a.countChildren(dir); got != 2 {
		t.Errorf("countChildren = %d, want 2 (hidden + .DS_Store excluded)", got)
	}
	if got := a.countChildren(filepath.Join(dir, "nope")); got != 0 {
		t.Errorf("missing dir should count 0, got %d", got)
	}
}

func TestCountOlderThan(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	old := filepath.Join(dir, "old.zip")
	fresh := filepath.Join(dir, "fresh.zip")
	os.WriteFile(old, []byte("x"), 0o644)
	os.WriteFile(fresh, []byte("x"), 0o644)
	// age `old` 200 days into the past; keep `fresh` at ~now.
	past := now.Add(-200 * 24 * time.Hour)
	os.Chtimes(old, past, past)
	os.Chtimes(fresh, now, now)

	a := &App{}
	if got := a.countOlderThan(dir, 90, now); got != 1 {
		t.Errorf("countOlderThan(90d) = %d, want 1", got)
	}
	if got := a.countOlderThan(dir, 365, now); got != 0 {
		t.Errorf("countOlderThan(365d) = %d, want 0", got)
	}
}

func TestNoteFor(t *testing.T) {
	if noteFor(engine.Outcome{Matched: []string{"/a", "/b"}}) != "2 removed" {
		t.Error("wrong note for matches")
	}
	if noteFor(engine.Outcome{}) != "no matches" {
		t.Error("wrong note for empty")
	}
	if got := noteFor(engine.Outcome{Err: os.ErrPermission}); got == "" {
		t.Error("error outcome should produce a note")
	}
}

// TestJobLifecycle exercises the full service path against an in-memory DB:
// add a job, list it, run a due expire job, and confirm it is retired.
func TestJobLifecycle(t *testing.T) {
	a, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer a.Close()

	t0 := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	// A watch job persists and lists.
	w := job.NewWatch(t.TempDir(), 30*24*time.Hour, job.BasisModified, job.ActionTrash, t0)
	if _, err := a.AddJob(w); err != nil {
		t.Fatalf("AddJob watch: %v", err)
	}
	// An expire job targeting a non-existent path: due later, retired on run.
	e := job.NewExpire(filepath.Join(t.TempDir(), "gone"), time.Hour, job.ActionTrash, t0)
	if _, err := a.AddJob(e); err != nil {
		t.Fatalf("AddJob expire: %v", err)
	}
	jobs, _ := a.Jobs()
	if len(jobs) != 2 {
		t.Fatalf("expected 2 jobs, got %d", len(jobs))
	}

	// Run everything due two hours later: the expire job fires (target gone →
	// no-op, no error) and is retired; the watch job remains.
	if _, err := a.RunDue(false, t0.Add(2*time.Hour)); err != nil {
		t.Fatalf("RunDue: %v", err)
	}
	jobs, _ = a.Jobs()
	if len(jobs) != 1 || jobs[0].Kind != job.KindWatch {
		t.Fatalf("expected only the watch job to remain, got %+v", jobs)
	}

	// Invalid job is rejected by AddJob.
	if _, err := a.AddJob(job.Job{}); err == nil {
		t.Error("AddJob should reject an invalid job")
	}
}
