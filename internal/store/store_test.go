package store

import (
	"testing"
	"time"

	"github.com/BadRat-in/fs-janitor/internal/job"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func sampleWatch(now time.Time) job.Job {
	j := job.NewWatch("/dl", 30*24*time.Hour, job.BasisModified, job.ActionTrash, now)
	j.Patterns = []string{"*.zip", "*.dmg"}
	j.Excludes = []string{"keep.*"}
	j.MinSizeKB = 1024
	j.Recursive = true
	return j
}

func TestSaveAndRoundTrip(t *testing.T) {
	s := open(t)
	now := time.Unix(1_700_000_000, 0)
	j, err := s.SaveJob(sampleWatch(now))
	if err != nil {
		t.Fatal(err)
	}
	if j.ID == 0 {
		t.Fatal("expected assigned ID")
	}
	got, ok, err := s.GetJob(j.ID)
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.Kind != job.KindWatch || got.Action != job.ActionTrash || got.Basis != job.BasisModified {
		t.Errorf("enums round-trip wrong: %+v", got)
	}
	if got.After != 30*24*time.Hour || got.MinSizeKB != 1024 || !got.Recursive {
		t.Errorf("fields round-trip wrong: %+v", got)
	}
	if len(got.Patterns) != 2 || got.Patterns[0] != "*.zip" || len(got.Excludes) != 1 {
		t.Errorf("slices round-trip wrong: %+v / %+v", got.Patterns, got.Excludes)
	}
}

func TestUpdateJob(t *testing.T) {
	s := open(t)
	j, _ := s.SaveJob(sampleWatch(time.Unix(1_700_000_000, 0)))
	j.Enabled = false
	j.Name = "renamed"
	if _, err := s.SaveJob(j); err != nil {
		t.Fatal(err)
	}
	got, _, _ := s.GetJob(j.ID)
	if got.Enabled || got.Name != "renamed" {
		t.Errorf("update not persisted: %+v", got)
	}
	all, _ := s.ListJobs()
	if len(all) != 1 {
		t.Errorf("update must not insert a new row, have %d", len(all))
	}
}

func TestDeleteJob(t *testing.T) {
	s := open(t)
	j, _ := s.SaveJob(sampleWatch(time.Unix(1, 0)))
	if err := s.DeleteJob(j.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.GetJob(j.ID); ok {
		t.Error("job still present after delete")
	}
	if err := s.DeleteJob(9999); err != nil {
		t.Errorf("deleting missing job should be a no-op, got %v", err)
	}
}

func TestListOrder(t *testing.T) {
	s := open(t)
	base := time.Unix(1_700_000_000, 0)
	a := job.NewWatch("/a", time.Hour, job.BasisModified, job.ActionTrash, base)
	b := job.NewWatch("/b", time.Hour, job.BasisModified, job.ActionTrash, base.Add(time.Hour))
	s.SaveJob(b) // save later-created first to prove ordering is by created_at
	s.SaveJob(a)
	list, _ := s.ListJobs()
	if len(list) != 2 || list[0].Path != "/a" {
		t.Fatalf("expected oldest-first order, got %+v", list)
	}
}

func TestHistoryAndTotals(t *testing.T) {
	s := open(t)
	t0 := time.Unix(1_700_000_000, 0)
	// two real runs + one dry run
	s.RecordRun(Run{JobID: 1, Kind: job.KindWatch, Target: "/dl", Files: 2, FreedKB: 500}, t0)
	s.RecordRun(Run{JobID: 1, Kind: job.KindWatch, Target: "/dl", Files: 1, FreedKB: 300}, t0.Add(time.Minute))
	s.RecordRun(Run{JobID: 1, Kind: job.KindWatch, Target: "/dl", Files: 9, FreedKB: 9999, DryRun: true}, t0.Add(2*time.Minute))

	total, err := s.TotalFreedKB()
	if err != nil {
		t.Fatal(err)
	}
	if total != 800 {
		t.Errorf("total freed = %d, want 800 (dry-run excluded)", total)
	}
	h, _ := s.History(2)
	if len(h) != 2 {
		t.Fatalf("limit ignored: got %d", len(h))
	}
	if !h[0].RanAt.After(h[1].RanAt) {
		t.Error("history should be newest-first")
	}
}

func TestRecordRunStampsLastRun(t *testing.T) {
	s := open(t)
	now := time.Unix(1_700_000_000, 0)
	j, _ := s.SaveJob(sampleWatch(now))
	ran := now.Add(time.Hour)
	if err := s.RecordRun(Run{JobID: j.ID, Kind: job.KindWatch, Target: j.Path, FreedKB: 10}, ran); err != nil {
		t.Fatal(err)
	}
	got, _, _ := s.GetJob(j.ID)
	if got.LastRun.Unix() != ran.Unix() {
		t.Errorf("last_run not stamped: %v want %v", got.LastRun, ran)
	}
	// dry run must NOT stamp last_run
	j2, _ := s.SaveJob(sampleWatch(now))
	s.RecordRun(Run{JobID: j2.ID, DryRun: true, Kind: job.KindWatch, Target: j2.Path}, ran)
	got2, _, _ := s.GetJob(j2.ID)
	if !got2.LastRun.IsZero() {
		t.Error("dry-run must not stamp last_run")
	}
}
