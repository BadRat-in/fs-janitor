// Package trash implements fs-janitor's "safe by default" deletion: instead of
// permanently removing a file or folder, the target is moved to the macOS Trash
// so the user can recover it later (Finder's Put-Back restores it to its
// original location).
//
// Two strategies are used, in order:
//
//  1. Primary — ask Finder to delete the item via osascript:
//     osascript -e 'tell application "Finder" to delete POSIX file "<abs-path>"'
//     This yields true Trash semantics, including the Put-Back metadata Finder
//     records for each trashed item.
//
//  2. Fallback — if osascript fails (Finder not running, automation permission
//     denied, headless session, etc.) the item is renamed into <home>/.Trash/
//     using a collision-safe destination name. This does not record Put-Back
//     information but still keeps the file recoverable rather than destroying it.
//     Note that os.Rename only works within a single filesystem/volume; cross-
//     volume moves fail here by design and surface as an error.
//
// Every side effect (command execution, rename, stat, home-directory lookup) is
// injected so the logic can be unit-tested without touching the real Trash or
// spawning osascript. ToTrash wires in the production seams; TrashWith exposes
// the same core with all seams as parameters.
package trash

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Runner executes an external command by name with the given arguments and
// returns any execution error (non-zero exit or spawn failure). It is the seam
// through which trashing shells out to osascript; tests substitute a fake that
// records the invocation instead of running anything.
//
// Parameters:
//   - name: the executable to run (e.g. "osascript").
//   - args: the arguments passed to that executable.
//
// Returns: nil on success, or the underlying execution error.
type Runner func(name string, args ...string) error

// OSRunner is the production Runner. It builds an *exec.Cmd and runs it to
// completion, discarding stdout/stderr and reporting only whether the command
// succeeded.
//
// Parameters:
//   - name: the executable to run.
//   - args: the arguments to pass.
//
// Returns: the error from (*exec.Cmd).Run — nil on a clean exit, or an
// *exec.ExitError / spawn error otherwise.
func OSRunner(name string, args ...string) error {
	return exec.Command(name, args...).Run()
}

// ToTrash moves the file or folder at path to the macOS Trash. It is the
// production entry point: the path is resolved to an absolute location and
// verified to exist, then Finder is asked (via osascript) to trash it, falling
// back to a rename into ~/.Trash on failure.
//
// Parameters:
//   - path: the file or directory to trash. Relative paths are resolved against
//     the current working directory.
//
// Returns: nil once the item has been trashed; a non-nil error if the path does
// not exist, the home directory cannot be determined, or both the osascript and
// the ~/.Trash fallback strategies fail.
//
// Edge cases: the path must exist before the call (a missing path is reported
// without attempting either strategy). The fallback rename is confined to a
// single volume, so trashing an item on a different volume than the home
// directory relies on osascript succeeding.
func ToTrash(path string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("trash: cannot determine home directory: %w", err)
	}
	return TrashWith(path, home, OSRunner, os.Rename, os.Stat)
}

// TrashWith is the fully injectable core of ToTrash, exposed so the engine (or
// tests) can supply fake seams. It resolves path to an absolute location,
// confirms the item exists, then attempts the osascript strategy and, on
// failure, the ~/.Trash rename fallback.
//
// Parameters:
//   - path: the file or directory to trash (resolved to absolute internally).
//   - home: the user's home directory; <home>/.Trash is the fallback location.
//   - run: seam used to invoke osascript.
//   - rename: seam used to move the item during the fallback (os.Rename in
//     production).
//   - stat: seam used both to verify the source exists and to probe for
//     collisions in the Trash (os.Stat in production).
//
// Returns: nil on success; an error if the path does not exist, or if both the
// osascript and fallback strategies fail (the fallback error is wrapped so the
// caller can see why safe deletion could not complete).
func TrashWith(path, home string, run Runner, rename func(old, new string) error, stat func(string) (os.FileInfo, error)) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("trash: cannot resolve path %q: %w", path, err)
	}

	// Verify the source exists up front so a typo is reported cleanly rather
	// than surfacing as an opaque osascript or rename failure.
	if _, err := stat(abs); err != nil {
		return fmt.Errorf("trash: path does not exist: %q: %w", abs, err)
	}

	// Primary strategy: let Finder trash the item, which records Put-Back info.
	script := fmt.Sprintf(`tell application "Finder" to delete POSIX file %q`, abs)
	if err := run("osascript", "-e", script); err == nil {
		return nil
	}

	// Fallback strategy: move the item into <home>/.Trash with a name that does
	// not clobber an existing entry.
	dest := trashDest(home, abs, stat)
	if err := rename(abs, dest); err != nil {
		return fmt.Errorf("trash: osascript failed and fallback move of %q to %q failed: %w", abs, dest, err)
	}
	return nil
}

// trashDest computes a collision-safe destination path inside <home>/.Trash for
// the item at abs. If <home>/.Trash/<base> is free it is used directly;
// otherwise a counter is inserted before the extension (name-1.ext, name-2.ext,
// …) until an unused name is found.
//
// Parameters:
//   - home: the user's home directory.
//   - abs: the absolute path of the item being trashed (only its base name is
//     used).
//   - stat: seam used to test whether a candidate destination already exists;
//     a non-nil error is treated as "free".
//
// Returns: an absolute path within <home>/.Trash that does not currently exist.
func trashDest(home, abs string, stat func(string) (os.FileInfo, error)) string {
	trashDir := filepath.Join(home, ".Trash")
	base := filepath.Base(abs)

	candidate := filepath.Join(trashDir, base)
	if _, err := stat(candidate); err != nil {
		// Nothing there (or unreadable) — safe to use as-is.
		return candidate
	}

	// Split the base name so the counter is inserted before the extension,
	// keeping the suffix meaningful (report-1.pdf rather than report.pdf-1).
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	for i := 1; ; i++ {
		candidate = filepath.Join(trashDir, fmt.Sprintf("%s-%d%s", stem, i, ext))
		if _, err := stat(candidate); err != nil {
			return candidate
		}
	}
}
