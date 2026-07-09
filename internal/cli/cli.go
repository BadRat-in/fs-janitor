// Package cli defines FS Janitor's command-line interface: the scriptable
// surface that mirrors every TUI action, built on Cobra.
//
// The design principle is "everything should be automatable" — the LaunchAgent
// runs `fsj run`, and power users script `fsj expire`, `fsj watch`, `fsj list`,
// `fsj clean`, `fsj score` and friends. Running `fsj` with no subcommand and an
// interactive terminal opens the full-screen TUI; in a pipe or CI it prints a
// read-only cleanup preview so the tool stays usable in scripts.
//
// Every command opens the shared app service (package app), performs its
// operation, and prints a plain-text result. Cobra wiring lives here; the actual
// work is delegated to app so the CLI and TUI never diverge.
package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/BadRat-in/fs-janitor/internal/app"
	"github.com/BadRat-in/fs-janitor/internal/duration"
	"github.com/BadRat-in/fs-janitor/internal/humanize"
	"github.com/BadRat-in/fs-janitor/internal/job"
	"github.com/BadRat-in/fs-janitor/internal/launchd"
	"github.com/BadRat-in/fs-janitor/internal/scan"
	"github.com/BadRat-in/fs-janitor/internal/tui"
	"github.com/spf13/cobra"
)

// Execute builds the command tree and runs it, returning the process exit code.
// version is stamped by the build and shown by `fsj version` / `--version`.
func Execute(version string) int {
	root := newRootCmd(version)
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "fsj:", err)
		return 1
	}
	return 0
}

// withApp opens the shared service, runs fn, and always closes the service.
func withApp(fn func(a *app.App) error) error {
	a, err := app.New("")
	if err != nil {
		return err
	}
	defer a.Close()
	return fn(a)
}

// newRootCmd assembles the root command and all subcommands.
func newRootCmd(version string) *cobra.Command {
	root := &cobra.Command{
		Use:           "fsj",
		Short:         "FS Janitor — the filesystem maintenance toolkit for macOS",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// No subcommand: TUI when interactive, preview otherwise.
			if isTerminal() {
				return withApp(func(a *app.App) error { return tui.Run(a, time.Now) })
			}
			return withApp(func(a *app.App) error {
				renderPreview(a.Scan(scan.NewOptions()))
				return nil
			})
		},
	}
	root.SetVersionTemplate("fsj {{.Version}}\n")
	root.AddCommand(
		newExpireCmd(), newWatchCmd(), newListCmd(), newRemoveCmd(),
		newRunCmd(), newCleanCmd(), newScoreCmd(), newHistoryCmd(),
		newInstallCmd(), newUninstallCmd(), newDoctorCmd(), newVersionCmd(version),
	)
	return root
}

// ---- expire ----

func newExpireCmd() *cobra.Command {
	var del, dry bool
	cmd := &cobra.Command{
		Use:   "expire <path> <duration>",
		Short: "Schedule a one-time expiration (e.g. delete a folder after 15d)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			d, err := duration.Parse(args[1])
			if err != nil {
				return err
			}
			return withApp(func(a *app.App) error {
				j := job.NewExpire(abs(args[0], a.Home), d, action(del), time.Now())
				j.DryRun = dry
				saved, err := a.AddJob(j)
				if err != nil {
					return err
				}
				fmt.Printf("Scheduled job #%d: %s\n", saved.ID, saved.Describe())
				return nil
			})
		},
	}
	cmd.Flags().BoolVar(&del, "delete", false, "permanently delete instead of moving to Trash")
	cmd.Flags().BoolVar(&dry, "dry-run", false, "preview only; never remove")
	return cmd
}

// ---- watch ----

func newWatchCmd() *cobra.Command {
	var after, from string
	var del, dry, recursive bool
	var patterns, excludes []string
	var minSizeKB int64
	cmd := &cobra.Command{
		Use:   "watch <path>",
		Short: "Continuously clean a directory (e.g. keep Downloads tidy)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			d, err := duration.Parse(after)
			if err != nil {
				return fmt.Errorf("--after: %w", err)
			}
			basis, err := parseBasis(from)
			if err != nil {
				return err
			}
			return withApp(func(a *app.App) error {
				j := job.NewWatch(abs(args[0], a.Home), d, basis, action(del), time.Now())
				j.Patterns, j.Excludes = patterns, excludes
				j.MinSizeKB, j.Recursive, j.DryRun = minSizeKB, recursive, dry
				saved, err := a.AddJob(j)
				if err != nil {
					return err
				}
				fmt.Printf("Created watcher #%d: %s\n", saved.ID, saved.Describe())
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&after, "after", "", "age threshold, e.g. 30d (required)")
	cmd.Flags().StringVar(&from, "from", "modified", "age basis: modified|birth|accessed")
	cmd.Flags().BoolVar(&del, "delete", false, "permanently delete instead of moving to Trash")
	cmd.Flags().BoolVar(&dry, "dry-run", false, "preview only; never remove")
	cmd.Flags().BoolVar(&recursive, "recursive", false, "descend into subdirectories")
	cmd.Flags().StringArrayVar(&patterns, "pattern", nil, "include glob (repeatable), e.g. --pattern '*.zip'")
	cmd.Flags().StringArrayVar(&excludes, "exclude", nil, "exclude glob (repeatable)")
	cmd.Flags().Int64Var(&minSizeKB, "min-size", 0, "only act on files at least this many KB")
	_ = cmd.MarkFlagRequired("after")
	return cmd
}

// ---- list / remove ----

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all maintenance jobs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return withApp(func(a *app.App) error {
				jobs, err := a.Jobs()
				if err != nil {
					return err
				}
				if len(jobs) == 0 {
					fmt.Println("No jobs. Create one with `fsj expire` or `fsj watch`.")
					return nil
				}
				for _, j := range jobs {
					state := "on "
					if !j.Enabled {
						state = "off"
					}
					fmt.Printf("#%-3d [%s] %s\n", j.ID, state, j.Describe())
				}
				return nil
			})
		},
	}
}

func newRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <id>...",
		Short: "Remove one or more jobs by ID",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withApp(func(a *app.App) error {
				for _, s := range args {
					id, err := strconv.ParseInt(s, 10, 64)
					if err != nil {
						return fmt.Errorf("invalid id %q", s)
					}
					if err := a.RemoveJob(id); err != nil {
						return err
					}
					fmt.Printf("Removed job #%d\n", id)
				}
				return nil
			})
		},
	}
}

// ---- run ----

func newRunCmd() *cobra.Command {
	var dry bool
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run every due job now (invoked by the LaunchAgent)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return withApp(func(a *app.App) error {
				results, err := a.RunDue(dry, time.Now())
				if err != nil {
					return err
				}
				var freed int64
				acted := 0
				for _, r := range results {
					if len(r.Matched) == 0 && r.Err == nil {
						continue
					}
					acted++
					freed += r.FreedKB
					fmt.Printf("• %-20s %3d item(s)  %s\n", r.Name, len(r.Matched), humanize.Size(r.FreedKB))
				}
				tag := ""
				if dry {
					tag = " (dry-run)"
				}
				fmt.Printf("Ran %d job(s); reclaimed %s%s\n", acted, humanize.Size(freed), tag)
				return nil
			})
		},
	}
	cmd.Flags().BoolVar(&dry, "dry-run", false, "preview only; never remove")
	return cmd
}

// ---- clean (read-only preview) ----

func newCleanCmd() *cobra.Command {
	var toolchains bool
	cmd := &cobra.Command{
		Use:   "clean",
		Short: "Preview reclaimable leftovers and caches (interactive cleanup is in the TUI)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return withApp(func(a *app.App) error {
				opts := scan.NewOptions()
				opts.IncludeToolchains = toolchains
				renderPreview(a.Scan(opts))
				return nil
			})
		},
	}
	cmd.Flags().BoolVar(&toolchains, "toolchains", false, "also list toolchain/runtime dirs")
	return cmd
}

// ---- score ----

func newScoreCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "score",
		Short: "Show the machine's maintenance score and storage health",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return withApp(func(a *app.App) error {
				r := a.Score(time.Now())
				fmt.Printf("Maintenance Score: %d/100 (%s)\n\n", r.Score, r.Grade)
				for _, c := range r.Categories {
					mark := "✓"
					if c.Warn {
						mark = "⚠"
					}
					line := fmt.Sprintf("  %s %-16s %s", mark, c.Name, c.Status)
					if c.Detail != "" {
						line += "  — " + c.Detail
					}
					fmt.Println(line)
				}
				fmt.Printf("\nPotential recovery: %s\n", humanize.Size(r.PotentialKB))
				fmt.Printf("Reclaimed all-time: %s\n", humanize.Size(r.LifetimeFreedKB))
				fmt.Printf("Active jobs:        %d\n", r.ActiveJobs)
				return nil
			})
		},
	}
}

// ---- history ----

func newHistoryCmd() *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "history",
		Short: "Show recent run history",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return withApp(func(a *app.App) error {
				runs, err := a.Store.History(limit)
				if err != nil {
					return err
				}
				if len(runs) == 0 {
					fmt.Println("No runs recorded yet.")
					return nil
				}
				for _, r := range runs {
					dry := ""
					if r.DryRun {
						dry = " (dry)"
					}
					fmt.Printf("%s  %-7s %8s  %d file(s)  %s%s\n",
						r.RanAt.Format("2006-01-02 15:04"), r.Kind,
						humanize.Size(r.FreedKB), r.Files, r.Target, dry)
				}
				return nil
			})
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 20, "maximum rows to show (0 = all)")
	return cmd
}

// ---- automation ----

func newInstallCmd() *cobra.Command {
	var interval int
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install the LaunchAgent so cleanup runs on a schedule",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			exe, err := os.Executable()
			if err != nil {
				return err
			}
			home, _ := os.UserHomeDir()
			logDir := home + "/Library/Application Support/fs-janitor"
			err = launchd.InstallDefault(home, launchd.Config{
				Label: launchd.Label, BinaryPath: exe, Args: []string{"run"},
				IntervalSeconds: interval,
				StdoutPath:      logDir + "/fsj.out.log", StderrPath: logDir + "/fsj.err.log",
			})
			if err != nil {
				return err
			}
			fmt.Printf("Automation installed — `fsj run` every %s.\n", duration.Format(time.Duration(interval)*time.Second))
			return nil
		},
	}
	cmd.Flags().IntVar(&interval, "interval", 3600, "seconds between runs")
	return cmd
}

func newUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the scheduled LaunchAgent",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			home, _ := os.UserHomeDir()
			if err := launchd.UninstallDefault(home); err != nil {
				return err
			}
			fmt.Println("Automation removed.")
			return nil
		},
	}
}

// ---- version ----

func newVersionCmd(version string) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version and exit",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("fsj %s\n", version)
			return nil
		},
	}
}

// ---- doctor ----

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check the environment and configuration",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("FS Janitor doctor")
			for _, tool := range []string{"du", "pgrep", "launchctl", "tmutil", "find", "osascript"} {
				status := "✓"
				if _, err := exec.LookPath(tool); err != nil {
					status = "✗ missing"
				}
				fmt.Printf("  %-10s %s\n", tool, status)
			}
			home, _ := os.UserHomeDir()
			fmt.Printf("  %-10s %s\n", "database", dbLabel(home))
			auto := "not installed"
			if launchd.IsInstalled(home, os.Stat) {
				auto = "installed"
			}
			fmt.Printf("  %-10s %s\n", "automation", auto)
			return nil
		},
	}
}

// ---- shared rendering / helpers ----

// renderPreview prints a plain, read-only cleanup scan grouped into sections.
func renderPreview(res scan.Result) {
	sections := []struct {
		title string
		kind  scan.Kind
	}{
		{"Developer / Build-Tool Caches", scan.KindDevCache},
		{"Toolchains / Runtimes", scan.KindToolchain},
		{"App Library Leftovers", scan.KindLibrary},
	}
	idx := 0
	for _, sec := range sections {
		var groups []scan.Group
		for _, g := range res.Groups {
			if g.Kind == sec.kind {
				groups = append(groups, g)
			}
		}
		if len(groups) == 0 {
			continue
		}
		sort.Slice(groups, func(i, j int) bool { return groups[i].SizeKB > groups[j].SizeKB })
		fmt.Printf("── %s ──\n", sec.title)
		for _, g := range groups {
			idx++
			fmt.Printf("%3d) %-32s %10s\n", idx, g.Vendor, humanize.Size(g.SizeKB))
		}
		fmt.Println()
	}
	if idx == 0 {
		fmt.Println("✅ Nothing to clean — your system looks tidy!")
		return
	}
	fmt.Printf("💾 Potential space to reclaim: %s\n", humanize.Size(res.TotalCandidateKB))
}

// action maps a --delete flag to the corresponding job action.
func action(del bool) job.Action {
	if del {
		return job.ActionDelete
	}
	return job.ActionTrash
}

// parseBasis converts a --from flag value to a job.Basis.
func parseBasis(s string) (job.Basis, error) {
	switch s {
	case "modified", "":
		return job.BasisModified, nil
	case "birth":
		return job.BasisBirth, nil
	case "accessed":
		return job.BasisAccessed, nil
	default:
		return "", fmt.Errorf("--from must be modified|birth|accessed, got %q", s)
	}
}

// abs makes a user-supplied path absolute, expanding a leading ~.
func abs(p, home string) string {
	if p == "~" {
		return home
	}
	if len(p) >= 2 && p[:2] == "~/" {
		return home + p[1:]
	}
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}

// dbLabel returns the on-disk database path label for doctor, honoring the
// FSJ_DB override when set.
func dbLabel(home string) string {
	if env := os.Getenv("FSJ_DB"); env != "" {
		return env
	}
	return home + "/Library/Application Support/fs-janitor/fsj.db"
}

// isTerminal reports whether stdout is an interactive terminal.
func isTerminal() bool {
	fi, err := os.Stdout.Stat()
	return err == nil && (fi.Mode()&os.ModeCharDevice) != 0
}
