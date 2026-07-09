// Package app is FS Janitor's service layer: the single seam where the reused
// CleanX scanning/cleanup stack, the new job/engine/store/analytics modules, and
// the macOS probes are wired together into high-level operations.
//
// Both entry points — the Cobra CLI (package cli) and the Bubble Tea TUI
// (package tui) — depend only on this package, never on the individual internal
// modules. That keeps the two front-ends thin and consistent: "create a job",
// "run everything due", "scan for cleanup", "compute the maintenance score" are
// defined once here and reused. Filesystem and database effects happen here;
// callers deal in plain results.
package app

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/BadRat-in/fs-janitor/internal/analytics"
	"github.com/BadRat-in/fs-janitor/internal/appindex"
	"github.com/BadRat-in/fs-janitor/internal/cleaner"
	"github.com/BadRat-in/fs-janitor/internal/config"
	"github.com/BadRat-in/fs-janitor/internal/detect"
	"github.com/BadRat-in/fs-janitor/internal/engine"
	"github.com/BadRat-in/fs-janitor/internal/job"
	"github.com/BadRat-in/fs-janitor/internal/osprobe"
	"github.com/BadRat-in/fs-janitor/internal/scan"
	"github.com/BadRat-in/fs-janitor/internal/store"
	"github.com/BadRat-in/fs-janitor/internal/tmutil"
	"github.com/BadRat-in/fs-janitor/internal/toolclean"
	"github.com/BadRat-in/fs-janitor/internal/trash"
)

// App bundles the resolved configuration and the wired collaborators. Build one
// with New and Close it when done (it owns the store handle).
type App struct {
	Home   string
	Cfg    *config.Config
	Store  *store.Store
	index  *appindex.Index
	engine *detect.Engine
	probes scan.Probes
	sizeKB func(string) int64
}

// New resolves the user's home, builds the cleanup config + installed-app index
// + detection engine + macOS probes, and opens the job/history database at
// dbPath (pass "" for the default location, or ":memory:" in tests). The
// returned App owns the store; call Close to release it.
func New(dbPath string) (*App, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("app: resolve home: %w", err)
	}
	cfg := config.Default(home)
	ix := appindex.Build(appindex.DefaultAppDirs(home), cfg.TokenStopwords, nil)
	probes := osprobe.New()
	if dbPath == "" {
		dbPath = store.DefaultPath(home)
	}
	st, err := store.Open(dbPath)
	if err != nil {
		return nil, err
	}
	return &App{
		Home:   home,
		Cfg:    cfg,
		Store:  st,
		index:  ix,
		engine: detect.New(ix, cfg),
		probes: probes,
		sizeKB: probes.SizeKB,
	}, nil
}

// Close releases owned resources (the database handle).
func (a *App) Close() error {
	if a.Store != nil {
		return a.Store.Close()
	}
	return nil
}

// ---- Cleanup (reused CleanX stack) ----

// Scan runs the leftover/dev-cache scan with the given options.
func (a *App) Scan(opts scan.Options) scan.Result {
	return scan.New(a.Cfg, a.engine, opts, a.probes).Scan()
}

// ToolCleanups returns the tool-native cache cleanups available on this machine.
func (a *App) ToolCleanups() []toolclean.Cleanup {
	return toolclean.Available(a.Home, a.sizeKB)
}

// DeleteCleanup permanently removes the given cleanup paths, returning freed-KB
// accounting. Used by the cleanup flow (which deletes caches outright).
func (a *App) DeleteCleanup(paths []string) cleaner.Result {
	return cleaner.DeletePaths(paths, a.sizeKB, cleaner.OSRemover)
}

// RunTool executes one tool-native cleanup, returning freed KB.
func (a *App) RunTool(c toolclean.Cleanup) (int64, error) {
	return c.Run(a.sizeKB, func(name string, args ...string) error {
		return quietRun(name, args...)
	})
}

// Snapshots lists local Time Machine snapshots.
func (a *App) Snapshots() []string {
	s, _ := tmutil.ListSnapshots(tmutil.OSRunner)
	return s
}

// DeleteSnapshot deletes one local Time Machine snapshot by id.
func (a *App) DeleteSnapshot(id string) error {
	return tmutil.DeleteSnapshot(tmutil.SudoRunner, id)
}

// ---- Jobs ----

// Jobs returns all persisted jobs.
func (a *App) Jobs() ([]job.Job, error) { return a.Store.ListJobs() }

// AddJob validates and persists a job, returning it with its assigned ID.
func (a *App) AddJob(j job.Job) (job.Job, error) {
	if err := j.Validate(); err != nil {
		return j, err
	}
	return a.Store.SaveJob(j)
}

// RemoveJob deletes a job by ID.
func (a *App) RemoveJob(id int64) error { return a.Store.DeleteJob(id) }

// SetJobEnabled toggles a job's enabled flag and persists it.
func (a *App) SetJobEnabled(id int64, enabled bool) error {
	j, ok, err := a.Store.GetJob(id)
	if err != nil || !ok {
		return err
	}
	j.Enabled = enabled
	_, err = a.Store.SaveJob(j)
	return err
}

// RunResult pairs an engine outcome with the human job name for reporting.
type RunResult struct {
	Name string
	engine.Outcome
}

// RunDue executes every enabled job that is due (all watch jobs run each cycle;
// expire jobs run only once their due time passes). Each run is recorded in the
// history table. forceDry overrides each job's own dry-run flag when true (used
// by `fsj run --dry-run` for a global preview). A one-time expire job that has
// fired (or whose target is already gone) on a real run is removed afterward.
// now is injected so the scheduler and tests control the clock.
func (a *App) RunDue(forceDry bool, now time.Time) ([]RunResult, error) {
	jobs, err := a.Store.ListJobs()
	if err != nil {
		return nil, err
	}
	env := a.engineEnv(now)
	var results []RunResult
	for _, j := range jobs {
		if !j.Enabled {
			continue
		}
		if j.Kind == job.KindExpire && !j.Due(now) {
			continue
		}
		if forceDry {
			j.DryRun = true
		}
		out := engine.Run(j, env)
		results = append(results, RunResult{Name: j.Name, Outcome: out})

		_ = a.Store.RecordRun(store.Run{
			JobID: j.ID, Kind: j.Kind, Target: j.Path,
			Files: len(out.Matched), FreedKB: out.FreedKB, Failed: out.Failed,
			DryRun: out.DryRun, Note: noteFor(out),
		}, now)

		// Retire a fired one-time expire job (real runs only).
		if j.Kind == job.KindExpire && !out.DryRun && out.Err == nil {
			_ = a.Store.DeleteJob(j.ID)
		}
	}
	return results, nil
}

// engineEnv builds the production engine environment with the safe Trash action
// and a permanent-delete fallback, clocked at now.
func (a *App) engineEnv(now time.Time) engine.Env {
	env := engine.ProductionEnv(a.sizeKB, trash.ToTrash, cleaner.OSRemover)
	env.Now = func() time.Time { return now }
	return env
}

// ---- Analytics ----

// Score runs a scan and gathers the ancillary signals to compute the machine's
// maintenance report. It is on-demand (a full scan is not cheap) and clocked at
// now for deterministic staleness counting.
func (a *App) Score(now time.Time) analytics.Report {
	res := a.Scan(scan.NewOptions())
	var devKB, libKB int64
	for _, g := range res.Groups {
		switch g.Kind {
		case scan.KindDevCache, scan.KindToolchain:
			devKB += g.SizeKB
		case scan.KindLibrary:
			libKB += g.SizeKB
		}
	}
	jobs, _ := a.Store.ListJobs()
	active := 0
	for _, j := range jobs {
		if j.Enabled {
			active++
		}
	}
	lifetime, _ := a.Store.TotalFreedKB()
	return analytics.Compute(analytics.Inputs{
		ReclaimableKB:   res.TotalCandidateKB,
		DevCacheKB:      devKB,
		LibraryKB:       libKB,
		StaleDownloads:  a.countOlderThan(filepath.Join(a.Home, "Downloads"), a.Cfg.StaleDays, now),
		DesktopClutter:  a.countChildren(filepath.Join(a.Home, "Desktop")),
		Snapshots:       len(a.Snapshots()),
		ActiveJobs:      active,
		LifetimeFreedKB: lifetime,
	})
}

// countOlderThan counts depth-1 files under dir whose mtime is older than
// staleDays as of now. Unreadable entries are ignored.
func (a *App) countOlderThan(dir string, staleDays int, now time.Time) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() || e.Name() == ".DS_Store" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if int(now.Sub(info.ModTime()).Hours()/24) > staleDays {
			n++
		}
	}
	return n
}

// countChildren counts non-hidden depth-1 entries under dir.
func (a *App) countChildren(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if e.Name() == ".DS_Store" || (len(e.Name()) > 0 && e.Name()[0] == '.') {
			continue
		}
		n++
	}
	return n
}

// noteFor summarizes an outcome for the history note column.
func noteFor(o engine.Outcome) string {
	if o.Err != nil {
		return "error: " + o.Err.Error()
	}
	if len(o.Matched) == 0 {
		return "no matches"
	}
	return fmt.Sprintf("%d removed", len(o.Matched))
}
