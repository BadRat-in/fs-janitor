// Package cleaner performs the actual deletion of selected leftover groups and
// reports how much space was reclaimed. Each path is measured immediately before
// removal (it is gone afterwards) and only counted toward the freed total on a
// successful delete, mirroring the accounting in the reference script.
//
// Removal and sizing are injected so the deletion bookkeeping is unit-tested
// without touching real files. The production Remover removes user-owned paths
// directly and escalates to sudo only when permission is denied.
package cleaner

import (
	"os"
	"os/exec"
)

// SizeFunc returns a path's size in kilobytes (du -sk semantics).
type SizeFunc func(path string) int64

// Remover deletes a path recursively, returning an error on failure.
type Remover func(path string) error

// PathResult records the outcome of removing one path.
type PathResult struct {
	Path    string
	Deleted bool
	SizeKB  int64
	Err     error
}

// Result aggregates a deletion run.
type Result struct {
	Paths   []PathResult
	FreedKB int64
}

// DeletePaths removes each path (measuring its size first) and returns per-path
// outcomes plus the total kilobytes reclaimed from successful deletions. Dry-run
// callers should not invoke this; they render the plan instead.
func DeletePaths(paths []string, sizeKB SizeFunc, remove Remover) Result {
	var res Result
	for _, p := range paths {
		if p == "" {
			continue
		}
		size := sizeKB(p)
		err := remove(p)
		pr := PathResult{Path: p, SizeKB: size, Err: err, Deleted: err == nil}
		if err == nil {
			res.FreedKB += size
		}
		res.Paths = append(res.Paths, pr)
	}
	return res
}

// OSRemover is the production Remover. It tries a direct recursive delete
// (sufficient for the user-owned ~/Library paths that make up the bulk of
// leftovers) and, only if that fails with a permission error, escalates to
// `sudo rm -rf` (needed for system-owned /Library paths). The sudo path may
// prompt for a password on the controlling terminal.
func OSRemover(path string) error {
	err := os.RemoveAll(path)
	if err == nil {
		return nil
	}
	if os.IsPermission(err) {
		return exec.Command("/usr/bin/sudo", "/bin/rm", "-rf", path).Run()
	}
	return err
}
