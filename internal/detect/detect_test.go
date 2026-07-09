package detect

import (
	"testing"

	"github.com/BadRat-in/fs-janitor/internal/appindex"
	"github.com/BadRat-in/fs-janitor/internal/config"
)

// buildEngine assembles an Engine over a synthetic installed-app set that mirrors
// the machine this feature was developed against: Google Drive and zoom.us are
// installed, but Chrome and Brave are NOT (they were uninstalled). This is the
// exact configuration where the old cleaner failed, so it's the right fixture.
func buildEngine() *Engine {
	cfg := config.Default("/Users/tester")
	ix := appindex.New(cfg.TokenStopwords)
	ix.AddApp("Google Drive", "com.google.drivefs")
	ix.AddApp("zoom.us", "us.zoom.xos")
	ix.AddApp("Mac Mouse Fix", "com.nuebling.mac-mouse-fix")
	ix.AddApp("Visual Studio Code", "com.microsoft.vscode")
	ix.AddApp("Spotify", "com.spotify.client")
	ix.AddApp("Slack", "com.tinyspeck.slackmacgap")
	// Note: no Chrome, no Brave, no Figma, no Discord installed.
	return New(ix, cfg)
}

// TestComponentInstalled covers the attribution matrix: what must be protected
// (installed) vs. what must be flaggable (not installed).
func TestComponentInstalled(t *testing.T) {
	e := buildEngine()
	cases := []struct {
		name string
		comp string
		want bool
	}{
		// --- must be protected (installed) ---
		{"drive bundle id exact", "com.google.drivefs", true},
		{"drive folder via hint", "drivefs", true},
		{"zoom bundle id", "us.zoom.xos", true},
		{"zoom helper via token", "us.zoom.ZoomDaemon", true},
		{"zoom updater via token", "ZoomUpdater", true},
		{"vscode plain folder Code via hint", "Code", true},
		{"vscode containment", "vscode", true},
		{"spotify helper prefix", "com.spotify.client.helper", true},
		{"mac mouse fix app segment", "com.nuebling.mac-mouse-fix", true},
		// GoogleUpdater / Keystone are shared: protected while ANY com.google.* installed.
		{"google updater wildcard hint", "com.google.GoogleUpdater", true},
		{"keystone wildcard hint", "keystone", true},

		// --- must be flaggable (uninstalled) ---
		{"chrome bundle id — uninstalled", "com.google.Chrome", false},
		{"brave bundle id — uninstalled", "com.brave.Browser", false},
		{"figma — uninstalled", "com.figma.desktop", false},
		{"discord — uninstalled", "com.hnc.discord", false},
		{"random vendor", "com.acme.widget", false},
	}
	for _, c := range cases {
		if got := e.ComponentInstalled(c.comp); got != c.want {
			t.Errorf("%s: ComponentInstalled(%q) = %v, want %v", c.name, c.comp, got, c.want)
		}
	}
}

// TestChromeVsDriveIsolation is the headline regression: Chrome must be flagged
// as a leftover even though its vendor sibling Google Drive is installed. The
// vendor segment ("google") is deliberately excluded from token matching so
// Drive's presence does not shield Chrome.
func TestChromeVsDriveIsolation(t *testing.T) {
	e := buildEngine()
	if e.ComponentInstalled("com.google.Chrome") {
		t.Fatal("com.google.Chrome must NOT be treated as installed while only Google Drive is present")
	}
	if !e.ComponentInstalled("com.google.drivefs") {
		t.Fatal("com.google.drivefs must be protected — Google Drive is installed")
	}
	if !e.ComponentConfirmedLeftover("com.google.Chrome") {
		t.Fatal("com.google.Chrome must be a confirmed leftover")
	}
}

// TestConfirmedLeftover checks the staleness-exemption gate: only positively
// attributable, uninstalled components qualify; unattributable plain names do
// not (they must fall back to the age rule).
func TestConfirmedLeftover(t *testing.T) {
	e := buildEngine()
	cases := []struct {
		comp string
		want bool
	}{
		{"com.google.Chrome", true},   // bundle id, uninstalled
		{"com.brave.Browser", true},   // bundle id, uninstalled
		{"com.google.drivefs", false}, // bundle id, but installed
		{"us.zoom.xos", false},        // bundle id, installed
		{"SomeRandomFolder", false},   // unattributable plain name
		{"Caches", false},             // unattributable plain name
	}
	for _, c := range cases {
		if got := e.ComponentConfirmedLeftover(c.comp); got != c.want {
			t.Errorf("ComponentConfirmedLeftover(%q) = %v, want %v", c.comp, got, c.want)
		}
	}
}

// TestWrapperExtensionStripping verifies plist / savedState / binarycookies
// suffixes are stripped before matching.
func TestWrapperExtensionStripping(t *testing.T) {
	e := buildEngine()
	if !e.ComponentInstalled("com.spotify.client.plist") {
		t.Error("com.spotify.client.plist should resolve to installed Spotify")
	}
	if !e.ComponentInstalled("us.zoom.xos.savedState") {
		t.Error("us.zoom.xos.savedState should resolve to installed zoom.us")
	}
	if e.ComponentInstalled("com.google.Chrome.binarycookies") {
		t.Error("com.google.Chrome.binarycookies should remain a leftover")
	}
}

// TestMemoization confirms repeated lookups are cached and stay consistent.
func TestMemoization(t *testing.T) {
	e := buildEngine()
	first := e.ComponentInstalled("com.google.Chrome")
	second := e.ComponentInstalled("com.google.Chrome")
	if first != second || first != false {
		t.Errorf("memoized result inconsistent: %v then %v", first, second)
	}
	if _, ok := e.cache["com.google.chrome"]; !ok {
		t.Error("expected normalized component to be memoized")
	}
}
