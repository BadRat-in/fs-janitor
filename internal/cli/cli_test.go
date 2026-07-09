package cli

import (
	"testing"

	"github.com/BadRat-in/fs-janitor/internal/job"
)

func TestAction(t *testing.T) {
	if action(true) != job.ActionDelete {
		t.Error("--delete should map to ActionDelete")
	}
	if action(false) != job.ActionTrash {
		t.Error("default should be ActionTrash")
	}
}

func TestParseBasis(t *testing.T) {
	cases := map[string]job.Basis{
		"":         job.BasisModified,
		"modified": job.BasisModified,
		"birth":    job.BasisBirth,
		"accessed": job.BasisAccessed,
	}
	for in, want := range cases {
		got, err := parseBasis(in)
		if err != nil || got != want {
			t.Errorf("parseBasis(%q) = %q,%v; want %q", in, got, err, want)
		}
	}
	if _, err := parseBasis("bogus"); err == nil {
		t.Error("expected error for unknown basis")
	}
}

func TestAbs(t *testing.T) {
	home := "/Users/tester"
	if got := abs("~", home); got != home {
		t.Errorf("abs(~) = %q, want %q", got, home)
	}
	if got := abs("~/Downloads", home); got != home+"/Downloads" {
		t.Errorf("abs(~/Downloads) = %q", got)
	}
	if got := abs("/tmp/x", home); got != "/tmp/x" {
		t.Errorf("abs(/tmp/x) = %q, want unchanged", got)
	}
}

func TestBuildCommandTree(t *testing.T) {
	root := newRootCmd("test")
	want := []string{"expire", "watch", "list", "remove", "run", "clean", "score", "history", "install", "uninstall", "doctor", "version"}
	have := map[string]bool{}
	for _, c := range root.Commands() {
		have[c.Name()] = true
	}
	for _, w := range want {
		if !have[w] {
			t.Errorf("missing subcommand %q", w)
		}
	}
}
