// Package job defines the central domain object of FS Janitor: the Job.
//
// A Job is the unit of filesystem maintenance the whole product is built
// around. It captures the PRD's Trigger → Target → Rule → Action model as a
// single, persistable value:
//
//   - Kind    — the trigger/shape: a one-time Expire or a recurring Watch.
//   - Path    — the target file or directory.
//   - Basis + After — the rule: how age is measured and the retention window.
//   - Action  — what happens to a match: move to Trash (safe default) or delete.
//
// This package is intentionally pure data + validation with no filesystem or
// database dependencies, so the model can be constructed, validated and unit
// tested in isolation. The engine (package engine) evaluates a Job against the
// filesystem; the store (package store) persists it. Both consume this type but
// this type imports neither.
package job

import (
	"fmt"
	"time"

	"github.com/BadRat-in/fs-janitor/internal/duration"
	"github.com/BadRat-in/fs-janitor/internal/humanize"
)

// Kind is the trigger/shape of a job.
type Kind string

const (
	// KindExpire is a one-time expiration: the entire target path is removed
	// once it reaches its due time. Models "delete this folder after 15 days".
	KindExpire Kind = "expire"
	// KindWatch is a recurring directory watcher: on every run, files inside the
	// target directory that satisfy the rule are removed. Models "keep Downloads
	// clean forever".
	KindWatch Kind = "watch"
)

// Action is what happens to a matched file or folder.
type Action string

const (
	// ActionTrash moves the item to the macOS Trash — the safe default, fully
	// recoverable by the user.
	ActionTrash Action = "trash"
	// ActionDelete permanently removes the item. Must be opted into explicitly.
	ActionDelete Action = "delete"
)

// Basis selects which timestamp a rule measures an item's age against.
type Basis string

const (
	// BasisModified measures age from the item's modification time (mtime).
	BasisModified Basis = "modified"
	// BasisBirth measures age from the item's creation time (APFS birth time).
	BasisBirth Basis = "birth"
	// BasisAccessed measures age from the item's last access time (atime).
	BasisAccessed Basis = "accessed"
	// BasisNow measures age from the moment the job was created. Used by expire
	// jobs to mean "N from now" regardless of the file's own timestamps.
	BasisNow Basis = "now"
)

// Job is a single maintenance rule. The zero value is not valid; use New* and
// Validate. Fields map directly onto the store schema so a Job round-trips
// through SQLite without a separate DTO.
type Job struct {
	ID        int64         // stable identifier assigned by the store (0 = unsaved)
	Name      string        // human label; defaults to the target basename
	Kind      Kind          // expire (one-time) or watch (recurring)
	Path      string        // absolute target file or directory
	Action    Action        // trash (default) or delete
	Basis     Basis         // how age is measured
	After     time.Duration // retention window / age threshold
	DueAt     time.Time     // expire jobs: when the target becomes eligible
	Patterns  []string      // watch: include globs (empty = all files)
	Excludes  []string      // watch: exclude globs
	MinSizeKB int64         // watch: only act on files at least this big (0 = any)
	Recursive bool          // watch: descend into subdirectories
	DryRun    bool          // preview only; never delete
	Enabled   bool          // disabled jobs are skipped by `fsj run`
	CreatedAt time.Time     // when the job was created
	LastRun   time.Time     // last time the engine executed this job (zero = never)
}

// NewExpire builds a one-time expiration job for path that becomes due `after`
// from now. Basis is BasisNow (measured from creation), Action defaults to
// Trash, and the job is enabled. The DueAt is computed from createdAt+after.
// createdAt is injected (rather than time.Now) so callers control the clock and
// tests stay deterministic.
func NewExpire(path string, after time.Duration, action Action, createdAt time.Time) Job {
	return Job{
		Name:      baseName(path),
		Kind:      KindExpire,
		Path:      path,
		Action:    defaultAction(action),
		Basis:     BasisNow,
		After:     after,
		DueAt:     createdAt.Add(after),
		Enabled:   true,
		CreatedAt: createdAt,
	}
}

// NewWatch builds a recurring directory-watcher job. On each run the engine
// removes files under path whose age (by basis) exceeds after. createdAt is
// injected for deterministic tests.
func NewWatch(path string, after time.Duration, basis Basis, action Action, createdAt time.Time) Job {
	return Job{
		Name:      baseName(path),
		Kind:      KindWatch,
		Path:      path,
		Action:    defaultAction(action),
		Basis:     defaultBasis(basis),
		After:     after,
		Enabled:   true,
		CreatedAt: createdAt,
	}
}

// Validate reports the first structural problem with a job, or nil if it is
// well-formed. It checks required fields and enum membership but performs no
// filesystem access (the path need not yet exist at validation time).
func (j Job) Validate() error {
	if j.Path == "" {
		return fmt.Errorf("job: path is required")
	}
	switch j.Kind {
	case KindExpire, KindWatch:
	default:
		return fmt.Errorf("job: unknown kind %q", j.Kind)
	}
	switch j.Action {
	case ActionTrash, ActionDelete:
	default:
		return fmt.Errorf("job: unknown action %q", j.Action)
	}
	switch j.Basis {
	case BasisModified, BasisBirth, BasisAccessed, BasisNow:
	default:
		return fmt.Errorf("job: unknown basis %q", j.Basis)
	}
	if j.After <= 0 {
		return fmt.Errorf("job: duration must be positive")
	}
	if j.Kind == KindExpire && j.DueAt.IsZero() {
		return fmt.Errorf("job: expire job has no due time")
	}
	return nil
}

// Due reports whether a one-time expire job has reached its due time as of now.
// It is meaningless for watch jobs (which run every cycle) and returns false
// for them.
func (j Job) Due(now time.Time) bool {
	if j.Kind != KindExpire {
		return false
	}
	return !now.Before(j.DueAt)
}

// Describe returns a one-line human summary of the job used in CLI and TUI
// listings, e.g. "watch  ~/Downloads  files >30d (modified) → trash".
func (j Job) Describe() string {
	switch j.Kind {
	case KindExpire:
		return fmt.Sprintf("expire %s in %s → %s", j.Path, duration.Format(j.After), j.Action)
	default:
		size := ""
		if j.MinSizeKB > 0 {
			size = " ≥" + humanize.Size(j.MinSizeKB)
		}
		return fmt.Sprintf("watch  %s  files >%s (%s)%s → %s",
			j.Path, duration.Format(j.After), j.Basis, size, j.Action)
	}
}

// defaultAction falls back to the safe Trash action when unset.
func defaultAction(a Action) Action {
	if a == "" {
		return ActionTrash
	}
	return a
}

// defaultBasis falls back to modification time when unset.
func defaultBasis(b Basis) Basis {
	if b == "" {
		return BasisModified
	}
	return b
}

// baseName returns the final path element, used as a job's default name.
func baseName(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[i+1:]
		}
	}
	return path
}
