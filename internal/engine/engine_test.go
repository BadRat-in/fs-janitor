package engine

import (
	"errors"
	"testing"
	"time"

	"github.com/BadRat-in/fs-janitor/internal/job"
)

func now() time.Time { return time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC) }

// fakeEnv records trash/delete calls and serves canned Stat/List data.
type fakeEnv struct {
	files    []FileInfo
	stat     map[string]FileInfo
	trashed  []string
	deleted  []string
	trashErr bool
	listErr  bool
}

func (f *fakeEnv) env() Env {
	return Env{
		Now:  now,
		Stat: func(p string) (FileInfo, bool) { fi, ok := f.stat[p]; return fi, ok },
		List: func(dir string, recursive bool) ([]FileInfo, error) {
			if f.listErr {
				return nil, errors.New("boom")
			}
			return f.files, nil
		},
		Trash: func(p string) error {
			if f.trashErr {
				return errors.New("nope")
			}
			f.trashed = append(f.trashed, p)
			return nil
		},
		Delete: func(p string) error { f.deleted = append(f.deleted, p); return nil },
	}
}

func TestRunExpireDueByNow(t *testing.T) {
	f := &fakeEnv{stat: map[string]FileInfo{"/t/a": {Path: "/t/a", SizeKB: 100}}}
	j := job.NewExpire("/t/a", time.Hour, job.ActionTrash, now().Add(-2*time.Hour)) // due already
	out := Run(j, f.env())
	if len(out.Matched) != 1 || out.FreedKB != 100 {
		t.Fatalf("expected 1 match/100KB, got %+v", out)
	}
	if len(f.trashed) != 1 {
		t.Errorf("expected trash call, got %v", f.trashed)
	}
}

func TestRunExpireNotYetDue(t *testing.T) {
	f := &fakeEnv{stat: map[string]FileInfo{"/t/a": {Path: "/t/a", SizeKB: 100}}}
	j := job.NewExpire("/t/a", 10*time.Hour, job.ActionTrash, now()) // due in 10h
	out := Run(j, f.env())
	if len(out.Matched) != 0 {
		t.Fatalf("should not act before due: %+v", out)
	}
}

func TestRunExpireTargetGone(t *testing.T) {
	f := &fakeEnv{stat: map[string]FileInfo{}} // stat miss
	j := job.NewExpire("/t/gone", time.Hour, job.ActionTrash, now().Add(-2*time.Hour))
	out := Run(j, f.env())
	if out.Err != nil || len(out.Matched) != 0 {
		t.Fatalf("missing target should be a no-op: %+v", out)
	}
}

func TestRunWatchAgeAndFilters(t *testing.T) {
	old := now().Add(-40 * 24 * time.Hour)
	fresh := now().Add(-1 * time.Hour)
	f := &fakeEnv{files: []FileInfo{
		{Path: "/dl/old.zip", SizeKB: 500, ModTime: old},
		{Path: "/dl/new.zip", SizeKB: 500, ModTime: fresh},
		{Path: "/dl/old.txt", SizeKB: 500, ModTime: old},
		{Path: "/dl/tiny.zip", SizeKB: 1, ModTime: old},
		{Path: "/dl/sub", IsDir: true, ModTime: old},
	}}
	j := job.NewWatch("/dl", 30*24*time.Hour, job.BasisModified, job.ActionDelete, now())
	j.Patterns = []string{"*.zip"}
	j.MinSizeKB = 10
	out := Run(j, f.env())
	// only old.zip qualifies: old enough, matches *.zip, big enough, not a dir.
	if len(out.Matched) != 1 || out.Matched[0] != "/dl/old.zip" {
		t.Fatalf("watch matched wrong set: %+v", out.Matched)
	}
	if len(f.deleted) != 1 {
		t.Errorf("expected 1 delete, got %v", f.deleted)
	}
}

func TestRunWatchExclude(t *testing.T) {
	old := now().Add(-40 * 24 * time.Hour)
	f := &fakeEnv{files: []FileInfo{
		{Path: "/dl/keep.zip", SizeKB: 10, ModTime: old},
		{Path: "/dl/drop.zip", SizeKB: 10, ModTime: old},
	}}
	j := job.NewWatch("/dl", 30*24*time.Hour, job.BasisModified, job.ActionTrash, now())
	j.Excludes = []string{"keep.*"}
	out := Run(j, f.env())
	if len(out.Matched) != 1 || out.Matched[0] != "/dl/drop.zip" {
		t.Fatalf("exclude failed: %+v", out.Matched)
	}
}

func TestRunDryRunNoEffects(t *testing.T) {
	old := now().Add(-40 * 24 * time.Hour)
	f := &fakeEnv{files: []FileInfo{{Path: "/dl/a", SizeKB: 20, ModTime: old}}}
	j := job.NewWatch("/dl", time.Hour, job.BasisModified, job.ActionDelete, now())
	j.DryRun = true
	out := Run(j, f.env())
	if len(out.Matched) != 1 || out.FreedKB != 20 {
		t.Fatalf("dry-run should report the match: %+v", out)
	}
	if len(f.deleted) != 0 || len(f.trashed) != 0 {
		t.Error("dry-run must not touch disk")
	}
}

func TestRunTrashFailureCounted(t *testing.T) {
	old := now().Add(-40 * 24 * time.Hour)
	f := &fakeEnv{files: []FileInfo{{Path: "/dl/a", SizeKB: 20, ModTime: old}}, trashErr: true}
	j := job.NewWatch("/dl", time.Hour, job.BasisModified, job.ActionTrash, now())
	out := Run(j, f.env())
	if out.Failed != 1 || out.FreedKB != 0 {
		t.Fatalf("failed trash should count and free nothing: %+v", out)
	}
}

func TestRunListError(t *testing.T) {
	f := &fakeEnv{listErr: true}
	j := job.NewWatch("/dl", time.Hour, job.BasisModified, job.ActionTrash, now())
	if out := Run(j, f.env()); out.Err == nil {
		t.Fatal("expected list error to surface")
	}
}

func TestRunInvalidJob(t *testing.T) {
	if out := Run(job.Job{}, (&fakeEnv{}).env()); out.Err == nil {
		t.Fatal("invalid job should return validation error")
	}
}

func TestUnreadableTimestampIsSafe(t *testing.T) {
	// zero ModTime => age unknown => never acted on.
	f := &fakeEnv{files: []FileInfo{{Path: "/dl/a", SizeKB: 20}}}
	j := job.NewWatch("/dl", time.Hour, job.BasisModified, job.ActionDelete, now())
	if out := Run(j, f.env()); len(out.Matched) != 0 {
		t.Fatalf("zero timestamp must not be deleted: %+v", out)
	}
}
