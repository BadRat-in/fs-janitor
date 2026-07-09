package scan

import (
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BadRat-in/fs-janitor/internal/config"
	"github.com/BadRat-in/fs-janitor/internal/detect"
)

// Options are the per-run toggles that mirror the reference script's settings
// menu. Zero value is the safe default set (see NewOptions).
type Options struct {
	IncludeInstalled  bool // also flag files for installed apps
	IncludeStale      bool // apply the staleness / activity gates
	CheckInUse        bool // skip files in use by processes / launchd
	IncludeToolchains bool // also list toolchain/runtime dirs (destructive)
}

// NewOptions returns the default options: staleness + in-use checks on, and
// installed apps + toolchains excluded — the conservative defaults.
func NewOptions() Options {
	return Options{IncludeInstalled: false, IncludeStale: true, CheckInUse: true, IncludeToolchains: false}
}

// Probes abstracts every filesystem/system interaction the scanner performs, so
// the scan logic can be exercised against a synthetic tree in tests. The real
// implementation (package osprobe / wired in cmd) shells out to du, pgrep,
// launchctl and stat, exactly like the reference script.
type Probes struct {
	// ListDir returns the depth-1 child paths of dir (full paths), or an error.
	ListDir func(dir string) ([]string, error)
	// IsDir reports whether path is a directory.
	IsDir func(path string) bool
	// Exists reports whether path exists.
	Exists func(path string) bool
	// SizeKB returns the size of path in kilobytes (du -sk semantics).
	SizeKB func(path string) int64
	// InUse reports whether path is actively used by a process or launchd.
	InUse func(path string) bool
	// ModTime returns path's mtime and whether it could be read.
	ModTime func(path string) (time.Time, bool)
	// RecentlyWritten reports whether any file under dir was modified within the
	// given number of days (dev-cache activity check).
	RecentlyWritten func(dir string, days int) bool
	// Now returns the current time (injected for deterministic staleness tests).
	Now func() time.Time
}

// Group is a vendor/tool bucket of leftover paths with an aggregate size.
type Group struct {
	Vendor string
	Paths  []string
	SizeKB int64
	// Kind classifies the group for sectioned display.
	Kind Kind
}

// Kind partitions results into the three display sections.
type Kind int

const (
	// KindLibrary is an app Library leftover.
	KindLibrary Kind = iota
	// KindDevCache is a re-downloadable developer cache.
	KindDevCache
	// KindToolchain is a runtime/toolchain dir (destructive to remove).
	KindToolchain
)

// Result is the outcome of a scan: groups keyed by vendor label, plus the
// aggregate candidate size across everything listed.
type Result struct {
	Groups           []Group
	TotalCandidateKB int64
}

// Scanner runs a scan against a config, detection engine, options and probes.
type Scanner struct {
	cfg    *config.Config
	engine *detect.Engine
	opts   Options
	probes Probes

	groups map[string]*Group
}

// New builds a Scanner. The engine must be constructed over the same config.
func New(cfg *config.Config, engine *detect.Engine, opts Options, probes Probes) *Scanner {
	return &Scanner{cfg: cfg, engine: engine, opts: opts, probes: probes, groups: map[string]*Group{}}
}

// Scan walks the Library scan dirs and the developer cache/toolchain dirs,
// applying the safety gates, and returns grouped results sorted by vendor name.
func (s *Scanner) Scan() Result {
	s.groups = map[string]*Group{}

	// ── Library dirs (depth 1, vendor containers expanded one level) ──────
	for _, dir := range s.cfg.ScanDirs {
		if !s.probes.Exists(dir) {
			continue
		}
		children, err := s.probes.ListDir(dir)
		if err != nil {
			continue
		}
		for _, item := range children {
			base := filepath.Base(item)
			if base == "" || s.ignored(base) {
				continue
			}
			// Expand known vendor container dirs one extra level so each app
			// inside is judged individually but still grouped under one vendor.
			if s.probes.IsDir(item) && s.cfg.VendorContainerDirs[strings.ToLower(base)] {
				grandkids, err := s.probes.ListDir(item)
				if err == nil {
					for _, child := range grandkids {
						s.processCandidate(child, dir)
					}
					continue
				}
			}
			s.processCandidate(item, dir)
		}
	}

	// ── Developer caches (+ toolchains when opted in) ─────────────────────
	devDirs := append([]string{}, s.cfg.DevCacheDirs...)
	kinds := make([]Kind, len(devDirs))
	for i := range kinds {
		kinds[i] = KindDevCache
	}
	if s.opts.IncludeToolchains {
		for _, d := range s.cfg.DevToolchainDirs {
			devDirs = append(devDirs, d)
			kinds = append(kinds, KindToolchain)
		}
	}
	for i, dd := range devDirs {
		if !s.probes.Exists(dd) {
			continue
		}
		if s.opts.CheckInUse && s.probes.InUse(dd) {
			continue
		}
		// Recent writes ⇒ tool in active use ⇒ leave it alone.
		if s.opts.IncludeStale && s.probes.RecentlyWritten(dd, s.cfg.DevStaleDays) {
			continue
		}
		label := s.devLabel(dd)
		s.add(label, dd, s.probes.SizeKB(dd), kinds[i])
	}

	return s.result()
}

// processCandidate evaluates one Library item and adds it to a group if it
// qualifies as a leftover. Mirrors process_candidate in the reference script.
func (s *Scanner) processCandidate(item, dir string) {
	comp := filepath.Base(item)
	if comp == "" {
		return
	}
	installed := s.engine.ComponentInstalled(comp)

	// Cache-like dirs skip the installed check (caches are fair game);
	// elsewhere never touch an installed app's files unless asked.
	if !s.isCacheDir(dir) {
		if !s.opts.IncludeInstalled && installed {
			return
		}
	}

	if s.opts.CheckInUse && s.probes.InUse(item) {
		return
	}

	// Staleness gate with the uninstalled-app exemption: a confirmed leftover of
	// an uninstalled app is flagged immediately; everything else needs age.
	if s.opts.IncludeStale {
		if installed || !s.engine.ComponentConfirmedLeftover(comp) {
			if !s.isStale(item) {
				return
			}
		}
	}

	vendor := GetVendorName(item, dir, s.cfg.VendorAliases)
	if vendor == "" {
		vendor = "Unknown"
	}
	s.add(vendor, item, s.probes.SizeKB(item), KindLibrary)
}

// isStale reports whether item's mtime is older than the configured threshold.
// An unreadable mtime is treated as NOT stale (safe: it won't be flagged).
func (s *Scanner) isStale(item string) bool {
	mt, ok := s.probes.ModTime(item)
	if !ok {
		return false
	}
	ageDays := int(s.probes.Now().Sub(mt).Hours() / 24)
	return ageDays > s.cfg.StaleDays
}

// isCacheDir reports whether dir is one of the cache-like roots.
func (s *Scanner) isCacheDir(dir string) bool {
	for _, cd := range s.cfg.CacheDirs {
		if dir == cd {
			return true
		}
	}
	return false
}

// ignored reports whether a depth-1 component matches any ignore substring.
func (s *Scanner) ignored(base string) bool {
	lower := strings.ToLower(base)
	for _, pat := range s.cfg.IgnorePatterns {
		if strings.Contains(lower, pat) {
			return true
		}
	}
	return false
}

// devLabel resolves a dev dir's display label, falling back to a title-cased
// basename (minus a leading dot) for paths not in the label map.
func (s *Scanner) devLabel(dir string) string {
	if l, ok := s.cfg.DevLabels[dir]; ok {
		return l
	}
	return titleCaseWords(strings.TrimPrefix(filepath.Base(dir), "."))
}

// add appends a path (and its size) to the named group, creating it if needed.
func (s *Scanner) add(vendor, path string, sizeKB int64, kind Kind) {
	g, ok := s.groups[vendor]
	if !ok {
		g = &Group{Vendor: vendor, Kind: kind}
		s.groups[vendor] = g
	}
	g.Paths = append(g.Paths, path)
	g.SizeKB += sizeKB
}

// result finalises the grouped map into a sorted slice, dropping empty (0 KB)
// groups and summing the candidate total.
func (s *Scanner) result() Result {
	var out []Group
	var total int64
	for _, g := range s.groups {
		if g.SizeKB == 0 {
			continue // skip zero-content groups, matching the script
		}
		out = append(out, *g)
		total += g.SizeKB
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Vendor < out[j].Vendor })
	return Result{Groups: out, TotalCandidateKB: total}
}
