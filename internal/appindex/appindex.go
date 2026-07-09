// Package appindex builds and holds the index of installed macOS applications
// that CleanX's detection engine consults to decide whether a Library item
// belongs to an app that is still present.
//
// The index is built once per run by scanning the standard application
// directories and, for each ".app" bundle, recording:
//
//   - the display name (raw lowercase and a normalized, de-punctuated form),
//   - name word tokens of length >= 4 (so a helper file like "ZoomDaemon" can
//     be tied back to "zoom.us"), excluding generic stopwords, and
//   - the real CFBundleIdentifier read from the bundle's Info.plist.
//
// Reading the *real* bundle ID (rather than guessing "/Applications/<Name>.app"
// from a file's basename) is the core correctness improvement over naive
// leftover cleaners: "com.google.Chrome" resolves to "Google Chrome.app"
// without any string gymnastics.
//
// The package is deliberately decoupled from the filesystem for testing: Build
// takes glob patterns and a BundleIDReader func, and AddApp lets tests assemble
// a synthetic index directly. Detection logic lives in package detect, not here.
package appindex

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Index is a set-based lookup of what is installed. All keys are lowercase.
type Index struct {
	// BundleIDs holds every installed app's CFBundleIdentifier, lowercased.
	BundleIDs map[string]bool
	// AppNames holds each app's display name, both raw-lowercase and normalized
	// (spaces, hyphens and underscores removed).
	AppNames map[string]bool
	// NameTokens holds app-name words of length >= 4 (minus stopwords), used to
	// attribute helper files back to their app.
	NameTokens map[string]bool

	stopwords map[string]bool
}

// New returns an empty Index. stopwords are name words too generic to be used
// as attribution tokens (e.g. "creator", "studio"); pass nil for none.
func New(stopwords map[string]bool) *Index {
	if stopwords == nil {
		stopwords = map[string]bool{}
	}
	return &Index{
		BundleIDs:  map[string]bool{},
		AppNames:   map[string]bool{},
		NameTokens: map[string]bool{},
		stopwords:  stopwords,
	}
}

// normalizeName lowercases a display name and strips spaces, hyphens and
// underscores, so "Mac Mouse Fix" and "mac-mouse-fix" both key as
// "macmousefix".
func normalizeName(name string) string {
	r := strings.NewReplacer(" ", "", "-", "", "_", "")
	return r.Replace(strings.ToLower(name))
}

// AddApp registers a single application by display name and (optionally) bundle
// ID. An empty bundleID is allowed — some system apps have no readable Info.plist
// — in which case only the name-based signals are recorded. This is the single
// entry point used by both Build and tests, so their behaviour cannot drift.
func (ix *Index) AddApp(name, bundleID string) {
	lower := strings.ToLower(name)
	ix.AppNames[lower] = true
	ix.AppNames[normalizeName(name)] = true

	// Tokenize on any non-alphanumeric run: "zoom.us" -> ["zoom", "us"].
	for _, tok := range strings.FieldsFunc(lower, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	}) {
		if len(tok) >= 4 && !ix.stopwords[tok] {
			ix.NameTokens[tok] = true
		}
	}

	if bundleID != "" {
		ix.BundleIDs[strings.ToLower(bundleID)] = true
	}
}

// BundleIDReader returns the CFBundleIdentifier for the ".app" bundle at
// appPath, or "" if it cannot be read. Injecting this makes Build testable and
// keeps the plist-reading strategy swappable.
type BundleIDReader func(appPath string) string

// DefaultAppDirs returns the glob patterns scanned for installed apps, matching
// the reference script: /Applications (one level deep), the user's
// ~/Applications, and /System/Applications (one level deep).
func DefaultAppDirs(home string) []string {
	return []string{
		"/Applications/*.app",
		"/Applications/*/*.app",
		filepath.Join(home, "Applications", "*.app"),
		"/System/Applications/*.app",
		"/System/Applications/*/*.app",
	}
}

// DefaultsBundleID reads CFBundleIdentifier via the macOS `defaults` tool, the
// same mechanism the reference script used. It targets "<app>/Contents/Info"
// (no .plist suffix, as `defaults` expects) and tolerates both XML and binary
// plists. Returns "" on any error.
func DefaultsBundleID(appPath string) string {
	info := filepath.Join(appPath, "Contents", "Info")
	out, err := exec.Command("/usr/bin/defaults", "read", info, "CFBundleIdentifier").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Build scans the given glob patterns for ".app" bundles and returns a
// populated Index, reading each bundle's ID via readID. Duplicate apps found
// through overlapping patterns are harmless (set semantics). A nil readID
// falls back to DefaultsBundleID.
func Build(patterns []string, stopwords map[string]bool, readID BundleIDReader) *Index {
	if readID == nil {
		readID = DefaultsBundleID
	}
	ix := New(stopwords)
	seen := map[string]bool{}
	for _, pat := range patterns {
		matches, err := filepath.Glob(pat)
		if err != nil {
			continue // a bad pattern shouldn't abort the whole index
		}
		for _, app := range matches {
			if seen[app] {
				continue
			}
			seen[app] = true
			// Only real bundles: a directory ending in .app.
			if fi, err := os.Stat(app); err != nil || !fi.IsDir() {
				continue
			}
			name := strings.TrimSuffix(filepath.Base(app), ".app")
			ix.AddApp(name, readID(app))
		}
	}
	return ix
}
