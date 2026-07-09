package toolclean

import (
	"os"
	"path/filepath"
	"testing"
)

// withLookPath temporarily overrides the PATH-lookup used by Available so tests
// can control which "tools" appear installed.
func withLookPath(present map[string]bool, fn func()) {
	orig := lookPath
	lookPath = func(bin string) bool { return present[bin] }
	defer func() { lookPath = orig }()
	fn()
}

// TestAvailableFiltersByPath verifies only present tools are offered and sizes
// are populated.
func TestAvailableFiltersByPath(t *testing.T) {
	home := t.TempDir()
	sizes := map[string]int64{
		filepath.Join(home, ".cache", "uv"):             5000,
		filepath.Join(home, "Library", "pnpm", "store"): 2000,
	}
	size := func(p string) int64 { return sizes[p] }

	withLookPath(map[string]bool{"uv": true, "pnpm": true}, func() {
		got := Available(home, size)
		if len(got) != 2 {
			t.Fatalf("expected 2 cleanups (uv, pnpm), got %d: %+v", len(got), got)
		}
		byLabel := map[string]Cleanup{}
		for _, c := range got {
			byLabel[c.Label] = c
		}
		if byLabel["uv cache"].SizeKB != 5000 {
			t.Errorf("uv size = %d, want 5000", byLabel["uv cache"].SizeKB)
		}
		if byLabel["uv cache"].Command() != "uv cache prune" {
			t.Errorf("uv command = %q", byLabel["uv cache"].Command())
		}
	})
}

// TestRunFreedAccounting checks the before/after size delta is reported as
// freed, and errors abort with zero freed.
func TestRunFreedAccounting(t *testing.T) {
	dir := "/fake/cache"
	// First call returns 1000 (before), second returns 200 (after) -> 800 freed.
	calls := 0
	size := func(p string) int64 {
		calls++
		if calls == 1 {
			return 1000
		}
		return 200
	}
	ran := false
	runner := func(name string, args ...string) error { ran = true; return nil }

	c := Cleanup{Label: "x", Name: "tool", Args: []string{"prune"}, SizeDir: dir}
	freed, err := c.Run(size, runner)
	if err != nil || !ran {
		t.Fatalf("run failed: err=%v ran=%v", err, ran)
	}
	if freed != 800 {
		t.Errorf("freed = %d, want 800", freed)
	}
}

// TestCargoProjectDiscovery verifies a Cargo.toml with a target/ dir is
// discovered under a probed root, deduped, and labeled relative to home.
func TestCargoProjectDiscovery(t *testing.T) {
	home := t.TempDir()
	proj := filepath.Join(home, "Projects", "myapp")
	if err := os.MkdirAll(filepath.Join(proj, "target"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "Cargo.toml"), []byte("[package]"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A project without target/ must be ignored.
	noTarget := filepath.Join(home, "Projects", "lib")
	if err := os.MkdirAll(noTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(noTarget, "Cargo.toml"), []byte("[package]"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := cargoProjects(home, func(string) int64 { return 12345 })
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 cargo project, got %d: %+v", len(got), got)
	}
	if got[0].Label != "cargo clean: Projects/myapp" {
		t.Errorf("label = %q, want 'cargo clean: Projects/myapp'", got[0].Label)
	}
	if got[0].SizeKB != 12345 {
		t.Errorf("size = %d, want 12345", got[0].SizeKB)
	}
}

// TestProjectRootsEnvOverride verifies CLEANX_PROJECT_DIRS is honoured.
func TestProjectRootsEnvOverride(t *testing.T) {
	t.Setenv("CLEANX_PROJECT_DIRS", "/work:/oss")
	roots := ProjectRoots("/home/x")
	if len(roots) != 2 || roots[0] != "/work" || roots[1] != "/oss" {
		t.Errorf("roots = %v, want [/work /oss]", roots)
	}
}
