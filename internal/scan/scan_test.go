package scan

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/BadRat-in/fs-janitor/internal/appindex"
	"github.com/BadRat-in/fs-janitor/internal/config"
	"github.com/BadRat-in/fs-janitor/internal/detect"
)

// fakeFS is an in-memory filesystem for driving the scanner deterministically.
type fakeFS struct {
	// children maps a dir to its depth-1 child paths.
	children map[string][]string
	// dirs is the set of paths that are directories.
	dirs map[string]bool
	// sizes maps a path to its KB size.
	sizes map[string]int64
	// mtimes maps a path to its modification time.
	mtimes map[string]time.Time
	// inUse is the set of paths reported as actively in use.
	inUse map[string]bool
	// recent maps a dev dir to whether it was written to recently.
	recent map[string]bool
	now    time.Time
}

func (f *fakeFS) probes() Probes {
	return Probes{
		ListDir: func(dir string) ([]string, error) { return f.children[dir], nil },
		IsDir:   func(p string) bool { return f.dirs[p] },
		Exists: func(p string) bool {
			if f.dirs[p] {
				return true
			}
			_, ok := f.sizes[p]
			return ok
		},
		SizeKB:          func(p string) int64 { return f.sizes[p] },
		InUse:           func(p string) bool { return f.inUse[p] },
		ModTime:         func(p string) (time.Time, bool) { t, ok := f.mtimes[p]; return t, ok },
		RecentlyWritten: func(dir string, days int) bool { return f.recent[dir] },
		Now:             func() time.Time { return f.now },
	}
}

// TestScanLibraryGates builds a synthetic ~/Library/Application Support with a
// mix of installed, uninstalled, ignored, in-use and fresh items and asserts
// exactly which vendors survive the gates.
func TestScanLibraryGates(t *testing.T) {
	home := "/Users/tester"
	cfg := config.Default(home)
	// Restrict the scan to a single dir for a focused test.
	appSupport := filepath.Join(home, "Library", "Application Support")
	cfg.ScanDirs = []string{appSupport}
	cfg.CacheDirs = nil
	cfg.DevCacheDirs = nil
	cfg.DevToolchainDirs = nil

	// Installed set: Google Drive + zoom.us (Chrome/Brave NOT installed).
	ix := appindex.New(cfg.TokenStopwords)
	ix.AddApp("Google Drive", "com.google.drivefs")
	ix.AddApp("zoom.us", "us.zoom.xos")
	ix.AddApp("Spotify", "com.spotify.client")
	engine := detect.New(ix, cfg)

	now := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	old := now.AddDate(0, 0, -200) // 200 days old -> stale
	fresh := now.AddDate(0, 0, -5) // 5 days old -> not stale

	p := func(name string) string { return filepath.Join(appSupport, name) }
	google := p("Google")

	fs := &fakeFS{
		now:  now,
		dirs: map[string]bool{appSupport: true, google: true},
		children: map[string][]string{
			appSupport: {
				p("com.brave.Browser"), // uninstalled -> confirmed leftover (fresh, but exempt)
				p("Spotify"),           // installed -> protected
				p("Evernote"),          // unattributed, old -> flagged by staleness
				p("FreshUnknown"),      // unattributed, fresh -> NOT flagged
				p("com.hnc.discord"),   // uninstalled but fresh + not stale... bundle id -> confirmed leftover, exempt
				p("AddressBook"),       // ignore pattern -> skipped
				p("com.tinyspeck.slack.inUse"),
				google, // vendor container -> expand
			},
			google: {
				filepath.Join(google, "Chrome"),  // hint -> uninstalled -> leftover
				filepath.Join(google, "DriveFS"), // hint -> installed -> protected
			},
		},
		sizes: map[string]int64{
			p("com.brave.Browser"):           1000,
			p("Spotify"):                     500,
			p("Evernote"):                    64,
			p("FreshUnknown"):                10,
			p("com.hnc.discord"):             300,
			p("AddressBook"):                 999,
			p("com.tinyspeck.slack.inUse"):   50,
			filepath.Join(google, "Chrome"):  9000,
			filepath.Join(google, "DriveFS"): 700,
		},
		mtimes: map[string]time.Time{
			p("com.brave.Browser"):           fresh,
			p("Spotify"):                     old,
			p("Evernote"):                    old,
			p("FreshUnknown"):                fresh,
			p("com.hnc.discord"):             fresh,
			p("com.tinyspeck.slack.inUse"):   old,
			filepath.Join(google, "Chrome"):  fresh,
			filepath.Join(google, "DriveFS"): old,
		},
		inUse: map[string]bool{p("com.tinyspeck.slack.inUse"): true},
	}

	sc := New(cfg, engine, NewOptions(), fs.probes())
	res := sc.Scan()

	got := map[string]int64{}
	for _, g := range res.Groups {
		got[g.Vendor] = g.SizeKB
	}

	// Must be present:
	wantPresent := map[string]int64{
		"Brave":    1000, // confirmed leftover, staleness-exempt
		"Evernote": 64,   // stale unattributed
		"Discord":  300,  // confirmed leftover, exempt despite being fresh
		"Google":   9000, // only Chrome; DriveFS protected
	}
	for v, sz := range wantPresent {
		if got[v] != sz {
			t.Errorf("group %q: got %d KB, want %d KB (groups: %v)", v, got[v], sz, got)
		}
	}

	// Must be absent:
	for _, v := range []string{"Spotify", "Fresh Unknown", "Freshunknown", "Addressbook", "Slack"} {
		if _, ok := got[v]; ok {
			t.Errorf("group %q should have been filtered out", v)
		}
	}

	// Google group must contain only Chrome's path, not DriveFS.
	for _, g := range res.Groups {
		if g.Vendor == "Google" {
			if len(g.Paths) != 1 || filepath.Base(g.Paths[0]) != "Chrome" {
				t.Errorf("Google group should contain only Chrome, got %v", g.Paths)
			}
		}
	}
}

// TestScanDevCaches verifies dev-cache inclusion, the recent-write skip, and the
// toolchain opt-in gate.
func TestScanDevCaches(t *testing.T) {
	home := "/Users/tester"
	cfg := config.Default(home)
	cfg.ScanDirs = nil
	cfg.CacheDirs = nil
	// Keep two caches + one toolchain.
	cargo := filepath.Join(home, ".cargo", "registry")
	npm := filepath.Join(home, ".npm")
	rustup := filepath.Join(home, ".rustup")
	cfg.DevCacheDirs = []string{cargo, npm}
	cfg.DevToolchainDirs = []string{rustup}

	ix := appindex.New(cfg.TokenStopwords)
	engine := detect.New(ix, cfg)

	fs := &fakeFS{
		now:  time.Now(),
		dirs: map[string]bool{cargo: true, npm: true, rustup: true},
		sizes: map[string]int64{
			cargo:  500000,
			npm:    2000,
			rustup: 1400000,
		},
		recent: map[string]bool{npm: true}, // npm written recently -> skipped
	}

	// Toolchains OFF by default: cargo listed, npm skipped (recent), rustup hidden.
	sc := New(cfg, engine, NewOptions(), fs.probes())
	res := sc.Scan()
	got := map[string]Kind{}
	for _, g := range res.Groups {
		got[g.Vendor] = g.Kind
	}
	if _, ok := got["Cargo Registry Cache"]; !ok {
		t.Error("Cargo Registry Cache should be listed")
	}
	if _, ok := got["npm Cache"]; ok {
		t.Error("npm Cache was written recently and must be skipped")
	}
	if _, ok := got["Rust Toolchains"]; ok {
		t.Error("Rust Toolchains must be hidden unless IncludeToolchains is set")
	}

	// Toolchains ON: rustup now appears, tagged KindToolchain.
	opts := NewOptions()
	opts.IncludeToolchains = true
	res = New(cfg, engine, opts, fs.probes()).Scan()
	found := false
	for _, g := range res.Groups {
		if g.Vendor == "Rust Toolchains" {
			found = true
			if g.Kind != KindToolchain {
				t.Errorf("Rust Toolchains kind = %v, want KindToolchain", g.Kind)
			}
		}
	}
	if !found {
		t.Error("Rust Toolchains should appear when IncludeToolchains is on")
	}
}
