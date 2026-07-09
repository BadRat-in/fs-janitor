package tui

import (
	"strings"
	"testing"

	"github.com/BadRat-in/fs-janitor/internal/job"
	"github.com/charmbracelet/bubbles/spinner"
)

// TestViewRendersWithoutPanic exercises the full chrome + every module and the
// modal overlays at several terminal sizes, asserting View never panics and
// always produces output. It uses a nil app because View performs no service
// calls (data is loaded via commands, not during render).
func TestViewRendersWithoutPanic(t *testing.T) {
	sizes := [][2]int{{100, 30}, {60, 20}, {20, 8}, {200, 50}}
	for _, sz := range sizes {
		for mod := module(0); mod < moduleCount; mod++ {
			m := Model{width: sz[0], height: sz[1], active: mod, spinner: spinner.New()}
			if out := m.View(); strings.TrimSpace(out) == "" {
				t.Errorf("empty view at %dx%d module %d", sz[0], sz[1], mod)
			}
		}
	}
	// Overlays.
	base := Model{width: 100, height: 30, spinner: spinner.New()}
	base.showHelp = true
	if base.View() == "" {
		t.Error("help overlay rendered empty")
	}
	base.showHelp = false
	base.confirm = "Delete everything?"
	if base.View() == "" {
		t.Error("confirm overlay rendered empty")
	}
	base.confirm = ""
	base.form = newJobForm()
	if base.View() == "" {
		t.Error("form rendered empty")
	}
}

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
