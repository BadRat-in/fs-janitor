package trash

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// errNotExist is a sentinel returned by stat fakes to mean "no such entry",
// mirroring how os.Stat reports a missing path.
var errNotExist = errors.New("not exist")

// TestTrashWithOsascriptSuccess verifies the primary strategy: when the runner
// succeeds, the osascript invocation is issued with the absolute path and a
// Finder-targeted script, and the fallback rename is never called.
func TestTrashWithOsascriptSuccess(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(src, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	var gotName string
	var gotArgs []string
	run := func(name string, args ...string) error {
		gotName = name
		gotArgs = args
		return nil
	}
	renameCalled := false
	rename := func(old, new string) error {
		renameCalled = true
		return nil
	}

	if err := TrashWith(src, dir, run, rename, os.Stat); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotName != "osascript" {
		t.Errorf("expected osascript, got %q", gotName)
	}
	joined := strings.Join(gotArgs, " ")
	if !strings.Contains(joined, "Finder") {
		t.Errorf("command should target Finder: %v", gotArgs)
	}
	if !strings.Contains(joined, src) {
		t.Errorf("command should contain abs path %q: %v", src, gotArgs)
	}
	if renameCalled {
		t.Error("rename must not be called when osascript succeeds")
	}
}

// TestTrashWithFallbackMovesToTrash verifies that when osascript fails the item
// is renamed into <home>/.Trash using its base name (no collision case).
func TestTrashWithFallbackMovesToTrash(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()

	run := func(name string, args ...string) error { return errors.New("finder unavailable") }

	var movedOld, movedNew string
	rename := func(old, new string) error {
		movedOld, movedNew = old, new
		return nil
	}
	// Source exists; Trash destination does not.
	stat := func(p string) (os.FileInfo, error) {
		if p == src {
			return nil, nil
		}
		return nil, errNotExist
	}

	if err := TrashWith(src, home, run, rename, stat); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if movedOld != src {
		t.Errorf("expected to move %q, moved %q", src, movedOld)
	}
	want := filepath.Join(home, ".Trash", "notes.md")
	if movedNew != want {
		t.Errorf("expected destination %q, got %q", want, movedNew)
	}
}

// TestTrashWithFallbackCollision verifies the collision-safe naming: when the
// plain destination and its first counter variant already exist, the counter is
// incremented and inserted before the extension.
func TestTrashWithFallbackCollision(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "report.pdf")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	trashDir := filepath.Join(home, ".Trash")

	run := func(name string, args ...string) error { return errors.New("boom") }

	var movedNew string
	rename := func(old, new string) error {
		movedNew = new
		return nil
	}

	// Simulate that report.pdf and report-1.pdf already exist in the Trash,
	// but report-2.pdf is free. The source always "exists".
	occupied := map[string]bool{
		filepath.Join(trashDir, "report.pdf"):   true,
		filepath.Join(trashDir, "report-1.pdf"): true,
	}
	stat := func(p string) (os.FileInfo, error) {
		if p == src {
			return nil, nil
		}
		if occupied[p] {
			return nil, nil
		}
		return nil, errNotExist
	}

	if err := TrashWith(src, home, run, rename, stat); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := filepath.Join(trashDir, "report-2.pdf")
	if movedNew != want {
		t.Errorf("expected collision-safe destination %q, got %q", want, movedNew)
	}
}

// TestTrashWithNonExistentPath verifies that a missing source is reported before
// either strategy runs: neither the runner nor the rename seam is invoked.
func TestTrashWithNonExistentPath(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "does-not-exist")

	runCalled := false
	run := func(name string, args ...string) error {
		runCalled = true
		return nil
	}
	renameCalled := false
	rename := func(old, new string) error {
		renameCalled = true
		return nil
	}
	stat := func(p string) (os.FileInfo, error) { return nil, errNotExist }

	err := TrashWith(missing, dir, run, rename, stat)
	if err == nil {
		t.Fatal("expected an error for a non-existent path")
	}
	if runCalled {
		t.Error("runner must not be called for a missing path")
	}
	if renameCalled {
		t.Error("rename must not be called for a missing path")
	}
}

// TestTrashWithFallbackRenameFails verifies that when both osascript and the
// fallback rename fail, the returned error surfaces the failure.
func TestTrashWithFallbackRenameFails(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "file.bin")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()

	run := func(name string, args ...string) error { return errors.New("no finder") }
	rename := func(old, new string) error { return errors.New("cross-device link") }
	stat := func(p string) (os.FileInfo, error) {
		if p == src {
			return nil, nil
		}
		return nil, errNotExist
	}

	if err := TrashWith(src, home, run, rename, stat); err == nil {
		t.Fatal("expected an error when both strategies fail")
	}
}
