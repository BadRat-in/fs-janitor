// Package toolclean discovers developer tools present on the machine and runs
// each tool's own cache-cleanup command (e.g. `uv cache prune`, `pnpm store
// prune`, `cargo clean`). Letting a tool prune itself is safer than `rm -rf`:
// it removes only what it can rebuild and keeps the tool's bookkeeping
// consistent. This mirrors the show_tool_cleanups step of the reference script.
//
// It also discovers Rust project build directories under common project roots
// (overridable via CLEANX_PROJECT_DIRS) and offers a per-project `cargo clean`,
// since target/ dirs are routinely the largest reclaimable space on a dev box.
//
// Command execution and sizing are injected (CmdRunner / SizeFunc) so discovery,
// selection, and freed-space accounting are unit-tested without touching the
// system.
package toolclean

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// CmdRunner executes a command, returning an error on non-zero exit. The
// production runner streams stdout/stderr to the user's terminal.
type CmdRunner func(name string, args ...string) error

// SizeFunc returns the size of a path in kilobytes (du -sk semantics).
type SizeFunc func(path string) int64

// Cleanup is one offered tool cleanup: a label, the command to run, and the
// directory whose size is measured before/after to report space reclaimed.
type Cleanup struct {
	Label   string
	Name    string   // command binary
	Args    []string // command arguments
	SizeDir string   // directory measured for size + freed accounting ("" if none)
	SizeKB  int64    // current size of SizeDir, filled by Available
}

// Command returns the human-readable command string shown to the user before
// they select a cleanup (e.g. "brew cleanup -s --prune=all").
func (c Cleanup) Command() string {
	return strings.TrimSpace(c.Name + " " + strings.Join(c.Args, " "))
}

// Run executes the cleanup via runner and returns the kilobytes reclaimed,
// measured as the drop in SizeDir's size (tools prune internally, so the
// listed size is not necessarily what is freed). A cleanup with no SizeDir
// reports 0 freed. runErr is the command's error, if any.
func (c Cleanup) Run(sizeKB SizeFunc, runner CmdRunner) (freedKB int64, runErr error) {
	var before int64
	if c.SizeDir != "" {
		before = sizeKB(c.SizeDir)
	}
	runErr = runner(c.Name, c.Args...)
	if runErr != nil {
		return 0, runErr
	}
	if c.SizeDir != "" {
		freed := before - sizeKB(c.SizeDir)
		if freed > 0 {
			freedKB = freed
		}
	}
	return freedKB, nil
}

// registry is the static set of tool cleanups, in display order. Only those
// whose binary is found on PATH are offered.
func registry(home string) []Cleanup {
	j := func(p ...string) string { return filepath.Join(append([]string{home}, p...)...) }
	return []Cleanup{
		{Label: "uv cache", Name: "uv", Args: []string{"cache", "prune"}, SizeDir: j(".cache", "uv")},
		{Label: "pnpm store", Name: "pnpm", Args: []string{"store", "prune"}, SizeDir: j("Library", "pnpm", "store")},
		{Label: "npm cache", Name: "npm", Args: []string{"cache", "clean", "--force"}, SizeDir: j(".npm", "_cacache")},
		{Label: "Yarn cache", Name: "yarn", Args: []string{"cache", "clean"}, SizeDir: j("Library", "Caches", "Yarn")},
		{Label: "Homebrew cache", Name: "brew", Args: []string{"cleanup", "-s", "--prune=all"}, SizeDir: j("Library", "Caches", "Homebrew")},
		{Label: "Go module cache", Name: "go", Args: []string{"clean", "-modcache"}, SizeDir: j("go", "pkg", "mod")},
		{Label: "CocoaPods cache", Name: "pod", Args: []string{"cache", "clean", "--all"}, SizeDir: j("Library", "Caches", "CocoaPods")},
		{Label: "Unavailable iOS simulators", Name: "xcrun", Args: []string{"simctl", "delete", "unavailable"}, SizeDir: ""},
	}
}

// lookPath reports whether a binary is on PATH; overridable in tests.
var lookPath = func(bin string) bool {
	_, err := exec.LookPath(bin)
	return err == nil
}

// Available returns the tool cleanups whose binaries exist, each sized via
// sizeKB, plus per-project `cargo clean` entries discovered under the project
// roots (only when the cargo binary is present).
func Available(home string, sizeKB SizeFunc) []Cleanup {
	var out []Cleanup
	for _, c := range registry(home) {
		if !lookPath(c.Name) {
			continue
		}
		if c.SizeDir != "" {
			c.SizeKB = sizeKB(c.SizeDir)
		}
		out = append(out, c)
	}
	if lookPath("cargo") {
		out = append(out, cargoProjects(home, sizeKB)...)
	}
	return out
}

// ProjectRoots returns the directories searched for Rust build dirs. It honours
// a colon-separated CLEANX_PROJECT_DIRS override; otherwise it probes common
// conventions (non-existent ones are skipped during discovery).
func ProjectRoots(home string) []string {
	if env := os.Getenv("CLEANX_PROJECT_DIRS"); env != "" {
		return strings.Split(env, ":")
	}
	names := []string{"Projects", "projects", "Developer", "Code", "code", "dev", "src", "repos", "workspace"}
	roots := make([]string, 0, len(names))
	for _, n := range names {
		roots = append(roots, filepath.Join(home, n))
	}
	return roots
}

// cargoProjects finds Cargo.toml files (up to 3 levels deep) that have a sibling
// target/ dir and returns a `cargo clean` Cleanup for each, deduped by
// lowercase path (APFS is case-insensitive, so ~/Projects and ~/projects would
// otherwise double-list the same project).
func cargoProjects(home string, sizeKB SizeFunc) []Cleanup {
	var out []Cleanup
	seen := map[string]bool{}
	for _, root := range ProjectRoots(home) {
		fi, err := os.Stat(root)
		if err != nil || !fi.IsDir() {
			continue
		}
		rootDepth := strings.Count(filepath.Clean(root), string(filepath.Separator))
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			// Limit descent to 3 levels below root.
			if d.IsDir() {
				if strings.Count(filepath.Clean(path), string(filepath.Separator))-rootDepth > 3 {
					return fs.SkipDir
				}
				return nil
			}
			if d.Name() != "Cargo.toml" {
				return nil
			}
			proj := filepath.Dir(path)
			target := filepath.Join(proj, "target")
			if fi, err := os.Stat(target); err != nil || !fi.IsDir() {
				return nil
			}
			key := strings.ToLower(proj)
			if seen[key] {
				return nil
			}
			seen[key] = true
			rel := strings.TrimPrefix(proj, home+string(filepath.Separator))
			out = append(out, Cleanup{
				Label:   "cargo clean: " + rel,
				Name:    "cargo",
				Args:    []string{"clean", "--manifest-path", path},
				SizeDir: target,
				SizeKB:  sizeKB(target),
			})
			return nil
		})
	}
	return out
}
