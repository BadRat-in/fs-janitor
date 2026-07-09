// Package engine executes jobs against the filesystem.
//
// It is the runtime that turns a declarative job.Job into filesystem effects:
// for an expire job it removes the whole target once due; for a watch job it
// walks the target directory and removes each file that satisfies the job's
// rule (age by basis, include/exclude globs, minimum size). Every effect is
// reported back in an Outcome so callers (the CLI `run` command, the TUI, and
// the history recorder) can show what happened and how much space was freed.
//
// All side effects are injected through Env, so the matching and accounting
// logic is pure and unit-tested against a synthetic file set. ProductionEnv
// wires Env to the real macOS filesystem, including APFS birth time and atime
// read via syscall, with removal delegated to the caller's trash/delete funcs.
package engine

import (
	"path/filepath"
	"time"

	"github.com/BadRat-in/fs-janitor/internal/job"
)

// FileInfo is the engine's view of one filesystem item. Times that cannot be
// read are left zero; the engine treats a zero basis time as "not old enough"
// so an unreadable timestamp never causes a deletion (fail safe).
type FileInfo struct {
	Path    string
	SizeKB  int64
	ModTime time.Time
	Birth   time.Time
	Access  time.Time
	IsDir   bool
}

// ageTime returns the timestamp this file should be aged against for the given
// basis. BasisNow has no per-file meaning (it is measured from job creation)
// and yields the zero time here; expire jobs handle BasisNow via DueAt instead.
func (f FileInfo) ageTime(b job.Basis) time.Time {
	switch b {
	case job.BasisBirth:
		return f.Birth
	case job.BasisAccessed:
		return f.Access
	case job.BasisModified:
		return f.ModTime
	default:
		return time.Time{}
	}
}

// Env injects every filesystem interaction the engine performs so the decision
// logic can be exercised against a synthetic tree. ProductionEnv supplies the
// live macOS implementations.
type Env struct {
	// Now returns the current time (injected for deterministic staleness tests).
	Now func() time.Time
	// Stat returns info for a single path and whether it exists/was readable.
	Stat func(path string) (FileInfo, bool)
	// List returns the immediate child files/dirs of dir (depth 1). When
	// recursive is true it returns all descendant files (dirs omitted).
	List func(dir string, recursive bool) ([]FileInfo, error)
	// Trash moves a path to the macOS Trash.
	Trash func(path string) error
	// Delete permanently removes a path.
	Delete func(path string) error
}

// Outcome is the result of running one job: the items acted on, space freed,
// failures, and whether it was a dry run. Matched holds the paths that were (or,
// in dry-run, would have been) removed.
type Outcome struct {
	JobID   int64
	Kind    job.Kind
	Matched []string
	FreedKB int64
	Failed  int
	DryRun  bool
	Err     error
}

// Run executes a single job against env and returns its Outcome. It never
// panics on a malformed job — it validates first and returns the error in the
// Outcome. Removal errors are counted in Failed rather than aborting the run,
// so one un-deletable file does not stop a watch sweep.
func Run(j job.Job, env Env) Outcome {
	out := Outcome{JobID: j.ID, Kind: j.Kind, DryRun: j.DryRun}
	if err := j.Validate(); err != nil {
		out.Err = err
		return out
	}
	switch j.Kind {
	case job.KindExpire:
		runExpire(j, env, &out)
	case job.KindWatch:
		runWatch(j, env, &out)
	}
	return out
}

// runExpire removes the entire target once the job is due. For BasisNow the due
// time is DueAt; for the timestamp bases the target's own age must exceed After.
func runExpire(j job.Job, env Env, out *Outcome) {
	now := env.Now()
	fi, ok := env.Stat(j.Path)
	if !ok {
		return // target already gone: nothing to do, not an error
	}
	eligible := false
	if j.Basis == job.BasisNow {
		eligible = j.Due(now)
	} else if t := fi.ageTime(j.Basis); !t.IsZero() {
		eligible = now.Sub(t) >= j.After
	}
	if !eligible {
		return
	}
	act(j, env, fi, out)
}

// runWatch walks the target directory and removes every file whose age by basis
// exceeds After and which passes the include/exclude/size filters.
func runWatch(j job.Job, env Env, out *Outcome) {
	now := env.Now()
	files, err := env.List(j.Path, j.Recursive)
	if err != nil {
		out.Err = err
		return
	}
	for _, fi := range files {
		if fi.IsDir {
			continue
		}
		if !matches(j, fi) {
			continue
		}
		t := fi.ageTime(j.Basis)
		if t.IsZero() || now.Sub(t) < j.After {
			continue
		}
		act(j, env, fi, out)
	}
}

// matches applies a watch job's non-age filters (min size, include globs,
// exclude globs) to a candidate file.
func matches(j job.Job, fi FileInfo) bool {
	if j.MinSizeKB > 0 && fi.SizeKB < j.MinSizeKB {
		return false
	}
	base := filepath.Base(fi.Path)
	for _, ex := range j.Excludes {
		if ok, _ := filepath.Match(ex, base); ok {
			return false
		}
	}
	if len(j.Patterns) == 0 {
		return true
	}
	for _, p := range j.Patterns {
		if ok, _ := filepath.Match(p, base); ok {
			return true
		}
	}
	return false
}

// act performs the job's action on one item, updating the outcome. In dry-run
// it records the match and its size without touching disk.
func act(j job.Job, env Env, fi FileInfo, out *Outcome) {
	out.Matched = append(out.Matched, fi.Path)
	if j.DryRun {
		out.FreedKB += fi.SizeKB
		return
	}
	var err error
	switch j.Action {
	case job.ActionDelete:
		err = env.Delete(fi.Path)
	default:
		err = env.Trash(fi.Path)
	}
	if err != nil {
		out.Failed++
		return
	}
	out.FreedKB += fi.SizeKB
}
