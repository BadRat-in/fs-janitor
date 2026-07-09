package tui

import (
	"testing"

	"github.com/BadRat-in/fs-janitor/internal/job"
)

func TestTruncate(t *testing.T) {
	if got := truncate("hello", 10); got != "hello" {
		t.Errorf("short string changed: %q", got)
	}
	if got := truncate("hello world", 5); got != "hell…" {
		t.Errorf("truncate = %q, want hell…", got)
	}
}

func TestPadRight(t *testing.T) {
	if got := padRight("ab", 5); got != "ab   " {
		t.Errorf("padRight = %q", got)
	}
	if got := padRight("abcdef", 3); got != "abcdef" {
		t.Errorf("over-width should be unchanged: %q", got)
	}
}

func TestExpandHome(t *testing.T) {
	home := "/Users/tester"
	if got := expandHome("~", home); got != home {
		t.Errorf("~ = %q", got)
	}
	if got := expandHome("~/Downloads", home); got != home+"/Downloads" {
		t.Errorf("~/Downloads = %q", got)
	}
	if got := expandHome("", home); got != "" {
		t.Errorf("empty should stay empty, got %q", got)
	}
}

func TestSplitFields(t *testing.T) {
	got := splitFields("*.zip, *.dmg   *.pkg")
	if len(got) != 3 || got[0] != "*.zip" || got[2] != "*.pkg" {
		t.Errorf("splitFields = %v", got)
	}
	if splitFields("   ") != nil {
		t.Error("blank should yield nil")
	}
}

func TestCycleBasis(t *testing.T) {
	seq := []job.Basis{
		cycleBasis(job.BasisModified),
		cycleBasis(job.BasisBirth),
		cycleBasis(job.BasisAccessed),
	}
	want := []job.Basis{job.BasisBirth, job.BasisAccessed, job.BasisModified}
	for i := range want {
		if seq[i] != want[i] {
			t.Errorf("cycle[%d] = %q, want %q", i, seq[i], want[i])
		}
	}
}

func TestStatusRun(t *testing.T) {
	if statusRun(0, 0) != "No jobs were due." {
		t.Errorf("zero-run status wrong: %q", statusRun(0, 0))
	}
	if statusRun(2, 2048) == "" {
		t.Error("non-zero run should produce a status")
	}
}

func TestFormActiveFields(t *testing.T) {
	f := newJobForm() // watch by default
	if !f.active(fBasis) || !f.active(fPat) || !f.active(fRec) {
		t.Error("watch form should expose basis/pattern/recursive")
	}
	f.kind = job.KindExpire
	if f.active(fBasis) || f.active(fPat) || f.active(fRec) {
		t.Error("expire form should hide basis/pattern/recursive")
	}
	if !f.active(fPath) || !f.active(fDur) || !f.active(fSubmit) {
		t.Error("core fields must stay active for expire")
	}
}
