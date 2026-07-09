// Package store is FS Janitor's persistence layer: an embedded SQLite database
// holding the user's jobs and the history of every run the engine performs.
//
// SQLite (via the pure-Go modernc.org/sqlite driver, so the binary stays
// cgo-free and statically linkable) is the right fit here — jobs are long-lived,
// queried and mutated from both the CLI and the TUI, and the run history needs
// durable aggregation for the analytics/maintenance-score views. The database
// lives under the user's Application Support directory.
//
// The package exposes a small, intention-revealing API (SaveJob, ListJobs,
// DeleteJob, RecordRun, History, TotalFreedKB) and keeps all SQL in one place so
// the schema is auditable. job.Job round-trips through the jobs table without a
// separate DTO; slice fields (patterns/excludes) are stored newline-joined.
package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BadRat-in/fs-janitor/internal/job"

	_ "modernc.org/sqlite" // pure-Go SQLite driver ("sqlite")
)

// Store is a handle to the open database. It is safe for sequential use from the
// CLI and the TUI's single event loop; it is not designed for concurrent
// writers (SQLite serializes them anyway).
type Store struct {
	db *sql.DB
}

// DefaultPath returns the standard on-disk location of the database for the
// given home directory: <home>/Library/Application Support/fs-janitor/fsj.db.
// The parent directory is not created here; Open handles that.
func DefaultPath(home string) string {
	return filepath.Join(home, "Library", "Application Support", "fs-janitor", "fsj.db")
}

// Open opens (creating if needed) the SQLite database at path, ensuring the
// parent directory exists and the schema is migrated. Callers must Close the
// returned Store. Passing ":memory:" yields an ephemeral database for tests.
func Open(path string) (*Store, error) {
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, fmt.Errorf("store: create data dir: %w", err)
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("store: open db: %w", err)
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the underlying database handle.
func (s *Store) Close() error { return s.db.Close() }

// migrate creates the schema if it does not yet exist. It is idempotent.
func (s *Store) migrate() error {
	const schema = `
CREATE TABLE IF NOT EXISTS jobs (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT    NOT NULL,
    kind        TEXT    NOT NULL,
    path        TEXT    NOT NULL,
    action      TEXT    NOT NULL,
    basis       TEXT    NOT NULL,
    after_sec   INTEGER NOT NULL,
    due_at      INTEGER NOT NULL DEFAULT 0,
    patterns    TEXT    NOT NULL DEFAULT '',
    excludes    TEXT    NOT NULL DEFAULT '',
    min_size_kb INTEGER NOT NULL DEFAULT 0,
    recursive   INTEGER NOT NULL DEFAULT 0,
    dry_run     INTEGER NOT NULL DEFAULT 0,
    enabled     INTEGER NOT NULL DEFAULT 1,
    created_at  INTEGER NOT NULL,
    last_run    INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS runs (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id   INTEGER NOT NULL,
    kind     TEXT    NOT NULL,
    target   TEXT    NOT NULL,
    ran_at   INTEGER NOT NULL,
    files    INTEGER NOT NULL,
    freed_kb INTEGER NOT NULL,
    failed   INTEGER NOT NULL DEFAULT 0,
    dry_run  INTEGER NOT NULL DEFAULT 0,
    note     TEXT    NOT NULL DEFAULT ''
);`
	_, err := s.db.Exec(schema)
	if err != nil {
		return fmt.Errorf("store: migrate: %w", err)
	}
	return nil
}

// SaveJob inserts a new job (ID == 0) or updates an existing one, returning the
// job with its assigned ID. Validation is the caller's responsibility; SaveJob
// persists whatever it is given.
func (s *Store) SaveJob(j job.Job) (job.Job, error) {
	if j.ID == 0 {
		res, err := s.db.Exec(
			`INSERT INTO jobs (name,kind,path,action,basis,after_sec,due_at,patterns,excludes,min_size_kb,recursive,dry_run,enabled,created_at,last_run)
			 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			j.Name, string(j.Kind), j.Path, string(j.Action), string(j.Basis),
			int64(j.After.Seconds()), unix(j.DueAt), joinLines(j.Patterns), joinLines(j.Excludes),
			j.MinSizeKB, b2i(j.Recursive), b2i(j.DryRun), b2i(j.Enabled), unix(j.CreatedAt), unix(j.LastRun),
		)
		if err != nil {
			return j, fmt.Errorf("store: insert job: %w", err)
		}
		id, _ := res.LastInsertId()
		j.ID = id
		return j, nil
	}
	_, err := s.db.Exec(
		`UPDATE jobs SET name=?,kind=?,path=?,action=?,basis=?,after_sec=?,due_at=?,patterns=?,excludes=?,min_size_kb=?,recursive=?,dry_run=?,enabled=?,created_at=?,last_run=? WHERE id=?`,
		j.Name, string(j.Kind), j.Path, string(j.Action), string(j.Basis),
		int64(j.After.Seconds()), unix(j.DueAt), joinLines(j.Patterns), joinLines(j.Excludes),
		j.MinSizeKB, b2i(j.Recursive), b2i(j.DryRun), b2i(j.Enabled), unix(j.CreatedAt), unix(j.LastRun), j.ID,
	)
	if err != nil {
		return j, fmt.Errorf("store: update job: %w", err)
	}
	return j, nil
}

// ListJobs returns all jobs ordered by creation time (oldest first).
func (s *Store) ListJobs() ([]job.Job, error) {
	rows, err := s.db.Query(`SELECT id,name,kind,path,action,basis,after_sec,due_at,patterns,excludes,min_size_kb,recursive,dry_run,enabled,created_at,last_run FROM jobs ORDER BY created_at ASC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("store: list jobs: %w", err)
	}
	defer rows.Close()
	var out []job.Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// GetJob returns the job with the given ID, or ok=false if none exists.
func (s *Store) GetJob(id int64) (job.Job, bool, error) {
	row := s.db.QueryRow(`SELECT id,name,kind,path,action,basis,after_sec,due_at,patterns,excludes,min_size_kb,recursive,dry_run,enabled,created_at,last_run FROM jobs WHERE id=?`, id)
	j, err := scanJob(row)
	if err == sql.ErrNoRows {
		return job.Job{}, false, nil
	}
	if err != nil {
		return job.Job{}, false, fmt.Errorf("store: get job: %w", err)
	}
	return j, true, nil
}

// DeleteJob removes a job by ID. Deleting a non-existent job is not an error.
func (s *Store) DeleteJob(id int64) error {
	_, err := s.db.Exec(`DELETE FROM jobs WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("store: delete job: %w", err)
	}
	return nil
}

// Run is one recorded execution of a job, used for history and analytics.
type Run struct {
	ID      int64
	JobID   int64
	Kind    job.Kind
	Target  string
	RanAt   time.Time
	Files   int
	FreedKB int64
	Failed  int
	DryRun  bool
	Note    string
}

// RecordRun appends a run-history row and, for a real (non-dry) run, stamps the
// job's last_run. ranAt is injected so callers control the clock.
func (s *Store) RecordRun(r Run, ranAt time.Time) error {
	_, err := s.db.Exec(
		`INSERT INTO runs (job_id,kind,target,ran_at,files,freed_kb,failed,dry_run,note) VALUES (?,?,?,?,?,?,?,?,?)`,
		r.JobID, string(r.Kind), r.Target, unix(ranAt), r.Files, r.FreedKB, r.Failed, b2i(r.DryRun), r.Note,
	)
	if err != nil {
		return fmt.Errorf("store: record run: %w", err)
	}
	if !r.DryRun && r.JobID != 0 {
		_, _ = s.db.Exec(`UPDATE jobs SET last_run=? WHERE id=?`, unix(ranAt), r.JobID)
	}
	return nil
}

// History returns the most recent runs, newest first, capped at limit (<=0
// means no cap).
func (s *Store) History(limit int) ([]Run, error) {
	q := `SELECT id,job_id,kind,target,ran_at,files,freed_kb,failed,dry_run,note FROM runs ORDER BY ran_at DESC, id DESC`
	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := s.db.Query(q)
	if err != nil {
		return nil, fmt.Errorf("store: history: %w", err)
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		var r Run
		var kind string
		var ranAt int64
		var dry int
		if err := rows.Scan(&r.ID, &r.JobID, &kind, &r.Target, &ranAt, &r.Files, &r.FreedKB, &r.Failed, &dry, &r.Note); err != nil {
			return nil, err
		}
		r.Kind = job.Kind(kind)
		r.RanAt = time.Unix(ranAt, 0)
		r.DryRun = dry != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// TotalFreedKB returns the lifetime sum of kilobytes reclaimed by real (non
// dry-run) runs — the headline "storage recovered" analytic.
func (s *Store) TotalFreedKB() (int64, error) {
	var total sql.NullInt64
	err := s.db.QueryRow(`SELECT SUM(freed_kb) FROM runs WHERE dry_run=0`).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("store: total freed: %w", err)
	}
	return total.Int64, nil
}

// scanner is satisfied by both *sql.Row and *sql.Rows so scanJob works for
// single-row and multi-row queries.
type scanner interface {
	Scan(dest ...any) error
}

// scanJob materializes one job row.
func scanJob(sc scanner) (job.Job, error) {
	var (
		j                          job.Job
		kind, action, basis        string
		afterSec, dueAt, createdAt int64
		lastRun                    int64
		patterns, excludes         string
		recursive, dryRun, enabled int
	)
	err := sc.Scan(&j.ID, &j.Name, &kind, &j.Path, &action, &basis, &afterSec, &dueAt,
		&patterns, &excludes, &j.MinSizeKB, &recursive, &dryRun, &enabled, &createdAt, &lastRun)
	if err != nil {
		return job.Job{}, err
	}
	j.Kind = job.Kind(kind)
	j.Action = job.Action(action)
	j.Basis = job.Basis(basis)
	j.After = time.Duration(afterSec) * time.Second
	j.DueAt = fromUnix(dueAt)
	j.Patterns = splitLines(patterns)
	j.Excludes = splitLines(excludes)
	j.Recursive = recursive != 0
	j.DryRun = dryRun != 0
	j.Enabled = enabled != 0
	j.CreatedAt = fromUnix(createdAt)
	j.LastRun = fromUnix(lastRun)
	return j, nil
}

// ---- small serialization helpers ----

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// unix returns t as unix seconds, or 0 for the zero time.
func unix(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

// fromUnix inverts unix, mapping 0 back to the zero time.
func fromUnix(sec int64) time.Time {
	if sec == 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0)
}

func joinLines(ss []string) string { return strings.Join(ss, "\n") }

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
