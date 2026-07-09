// Package osprobe provides the production implementation of scan.Probes: the
// real filesystem and system interactions the scanner needs on macOS. It shells
// out to the same tools the reference CleanX.zsh used (du, pgrep, launchctl,
// find) so sizing and in-use detection behave identically, and uses the Go
// standard library for directory listing, stat, and mtime.
//
// Keeping these behind the scan.Probes interface means the scanner's decision
// logic stays pure and unit-tested (package scan), while this package — which is
// inherently tied to the host and hard to unit-test — stays a thin, auditable
// adapter.
package osprobe

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/BadRat-in/fs-janitor/internal/scan"
)

// New returns a scan.Probes wired to the live macOS system.
func New() scan.Probes {
	return scan.Probes{
		ListDir:         listDir,
		IsDir:           isDir,
		Exists:          exists,
		SizeKB:          sizeKB,
		InUse:           inUse,
		ModTime:         modTime,
		RecentlyWritten: recentlyWritten,
		Now:             time.Now,
	}
}

// listDir returns the depth-1 child paths of dir as absolute paths. A read
// error yields an empty slice so one unreadable dir never aborts the scan.
func listDir(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, filepath.Join(dir, e.Name()))
	}
	return out, nil
}

// isDir reports whether path is a directory (symlinks followed, matching the
// script's test -d semantics).
func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// exists reports whether path exists (following symlinks).
func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// sizeKB returns path's on-disk size in kilobytes via `du -sk`, matching the
// script. On any error it returns 0 (the scanner drops zero-size groups).
func sizeKB(path string) int64 {
	out, err := exec.Command("/usr/bin/du", "-sk", path).Output()
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return 0
	}
	n, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// inUse reports whether path is actively used by a running process, or — for a
// launchd .plist — by a loaded launch service. Mirrors is_in_use: generic
// basenames are NOT grepped against launchctl (that produced false positives),
// only .plist basenames are.
func inUse(path string) bool {
	if exec.Command("/usr/bin/pgrep", "-qf", path).Run() == nil {
		return true
	}
	base := filepath.Base(path)
	if strings.HasSuffix(base, ".plist") {
		name := strings.TrimSuffix(base, ".plist")
		out, err := exec.Command("/bin/launchctl", "list").Output()
		if err == nil && strings.Contains(strings.ToLower(string(out)), strings.ToLower(name)) {
			return true
		}
	}
	return false
}

// modTime returns path's modification time. The bool is false when it can't be
// stat'd, which the scanner treats as "not stale" (safe: it won't be flagged).
func modTime(path string) (time.Time, bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return time.Time{}, false
	}
	return fi.ModTime(), true
}

// recentlyWritten reports whether any file under dir was modified within the
// last `days` days, using `find -mtime -N -print -quit` so it stops at the
// first hit — cheap even on huge trees. mtime only (atime is unreliable on
// APFS; build tools write their caches when used).
func recentlyWritten(dir string, days int) bool {
	out, err := exec.Command(
		"/usr/bin/find", dir, "-type", "f",
		"-mtime", "-"+strconv.Itoa(days), "-print", "-quit",
	).Output()
	if err != nil {
		return false
	}
	return len(strings.TrimSpace(string(out))) > 0
}
