// Package config holds the static configuration that drives CleanX's leftover
// detection: which directories to scan, which patterns to protect, how folder
// and bundle-ID names map to applications, and the age thresholds that gate
// deletion.
//
// This is a direct, auditable port of the configuration blocks at the top of
// the reference CleanX.zsh script. Keeping it as plain data (rather than logic)
// means the safety-critical lists — ignore patterns, Apple system-data stores,
// app hints — can be reviewed in one place and unit-tested without touching the
// filesystem. The detection engine (package detect) consumes this data; it does
// not embed any of these values itself.
//
// All map keys that represent a filesystem component or bundle-ID token are
// stored lowercase; callers must lowercase their input before lookup. Helper
// constructors (Default*) return fresh copies so tests can mutate them freely.
package config

import (
	"os"
	"path/filepath"
	"regexp"
)

// BundleIDRegex matches reverse-domain (bundle-ID style) names by their TLD
// prefix, e.g. "com.spotify.client" or "org.mozilla.firefox". A leading match
// is what distinguishes an attributable bundle-ID component from a plain
// folder name like "Kitty". Mirrors BUNDLE_ID_REGEX in the script.
var BundleIDRegex = regexp.MustCompile(
	`^(com|org|io|net|us|co|app|uk|de|fr|pro|me|build|tools|dev|sh|it|be|nl|at)\.`,
)

// Staleness thresholds, in days. See Config for meaning.
const (
	// DefaultStaleDays is how long an unattributed Library item must have been
	// untouched before it is eligible for deletion. Items positively attributed
	// to an uninstalled app bypass this gate entirely.
	DefaultStaleDays = 90

	// DefaultDevStaleDays is the activity window for developer caches: a cache
	// written to within this many days is treated as actively used and skipped.
	DefaultDevStaleDays = 30
)

// Config is the resolved, per-run configuration. Directory lists are computed
// against the supplied home directory so the same struct can be built for the
// current user or, in tests, for a synthetic home.
type Config struct {
	Home string

	// ScanDirs are the standard Library locations scanned at depth 1.
	ScanDirs []string
	// CacheDirs are the subset of ScanDirs where the installed-app check is
	// skipped (an installed app's caches are fair game once stale).
	CacheDirs []string

	// DevCacheDirs are re-downloadable build-tool caches, scoped to true cache
	// subpaths so deletion never removes a tool binary, its config, or creds.
	DevCacheDirs []string
	// DevToolchainDirs are runtime/toolchain dirs whose deletion uninstalls the
	// tool or its language versions; offered only when the user opts in.
	DevToolchainDirs []string
	// DevLabels maps a dev cache/toolchain path to its human-readable label
	// (e.g. ~/.cargo/registry -> "Cargo Registry Cache"). Paths not present fall
	// back to a title-cased basename.
	DevLabels map[string]string

	// VendorContainerDirs are depth-1 folders (lowercased names) that hold
	// per-app subfolders and are expanded one extra level during scanning.
	VendorContainerDirs map[string]bool

	// PlainDirAppHints maps a lowercase folder/plist component to the lowercase
	// bundle ID of its owning app. A value ending in "*" is a prefix wildcard
	// matching any installed bundle ID under that prefix (for vendor-shared
	// components such as updaters).
	PlainDirAppHints map[string]string

	// VendorAliases canonicalises vendor tokens so bundle-ID and folder-name
	// spellings of one vendor group together (lowercase token -> display name).
	VendorAliases map[string]string

	// IgnorePatterns are substrings (lowercase) that, if contained in a depth-1
	// component, cause it to be skipped — Apple/system components and plain-named
	// OS data stores.
	IgnorePatterns []string

	// TokenStopwords are app-name words too generic to identify an app; never
	// used as protection tokens.
	TokenStopwords map[string]bool

	// StaleDays / DevStaleDays are the active thresholds (see constants above).
	StaleDays    int
	DevStaleDays int
}

// Default builds a Config for the given home directory with the same values the
// reference script uses. Passing an empty home falls back to os.UserHomeDir.
func Default(home string) *Config {
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	j := func(parts ...string) string { return filepath.Join(append([]string{home}, parts...)...) }

	return &Config{
		Home: home,
		ScanDirs: []string{
			j("Library", "Application Support"),
			"/Library/Application Support",
			j("Library", "LaunchAgents"),
			"/Library/LaunchAgents",
			"/Library/LaunchDaemons",
			j("Library", "Caches"),
			"/Library/Caches",
			j("Library", "Preferences"),
			j("Library", "HTTPStorages"),
			j("Library", "Saved Application State"),
			j("Library", "WebKit"),
			j("Library", "Logs"),
		},
		CacheDirs: []string{
			j("Library", "Caches"),
			"/Library/Caches",
			j("Library", "Logs"),
		},
		DevCacheDirs: []string{
			j(".gradle", "caches"),
			j(".gradle", "wrapper"),
			j(".android", "cache"),
			j(".npm"),
			j(".pub-cache"),
			j(".cargo", "registry"),
			j(".cargo", "git"),
			j(".yarn"),
			j(".dartServer"),
			j(".cache"),
			j(".bun", "install", "cache"),
			j(".cocoapods"),
			j(".m2", "repository"),
			j(".ivy2", "cache"),
			j("Library", "Developer", "Xcode", "DerivedData"),
			j("Library", "Developer", "Xcode", "iOS DeviceSupport"),
			j("Library", "Developer", "CoreSimulator", "Caches"),
		},
		DevToolchainDirs: []string{
			j(".rustup"),
			j(".nvm", "versions"),
			j(".pyenv", "versions"),
			j(".rbenv", "versions"),
			j(".gem"),
			j(".android", "avd"),
			j("Library", "Developer", "CoreSimulator", "Devices"),
		},
		DevLabels: map[string]string{
			j(".gradle", "caches"):        "Gradle Build Cache",
			j(".gradle", "wrapper"):       "Gradle Wrappers",
			j(".android", "cache"):        "Android SDK Cache",
			j(".android", "avd"):          "Android Emulators (AVD)",
			j(".npm"):                     "npm Cache",
			j(".pub-cache"):               "Dart/Flutter Pub Cache",
			j(".cargo", "registry"):       "Cargo Registry Cache",
			j(".cargo", "git"):            "Cargo Git Cache",
			j(".rustup"):                  "Rust Toolchains",
			j(".yarn"):                    "Yarn Cache",
			j(".dartServer"):              "Dart Server Cache",
			j(".cache"):                   "XDG Cache",
			j(".bun", "install", "cache"): "Bun Cache",
			j(".nvm", "versions"):         "NVM Node Versions",
			j(".cocoapods"):               "CocoaPods Cache",
			j(".m2", "repository"):        "Maven Repository",
			j(".ivy2", "cache"):           "Ivy/sbt Cache",
			j(".pyenv", "versions"):       "pyenv Pythons",
			j(".rbenv", "versions"):       "rbenv Rubies",
			j(".gem"):                     "Ruby Gems",
			j("Library", "Developer", "Xcode", "DerivedData"):       "Xcode DerivedData",
			j("Library", "Developer", "Xcode", "iOS DeviceSupport"): "Xcode iOS DeviceSupport",
			j("Library", "Developer", "CoreSimulator", "Caches"):    "Xcode Simulator Cache",
			j("Library", "Developer", "CoreSimulator", "Devices"):   "Xcode Simulators",
		},
		VendorContainerDirs: map[string]bool{
			"google":        true,
			"bravesoftware": true,
			"mozilla":       true,
			"jetbrains":     true,
			"microsoft":     true,
		},
		PlainDirAppHints: map[string]string{
			"google":                    "com.google.chrome",
			"chrome":                    "com.google.chrome",
			"googleupdater":             "com.google.*",
			"com.google.googleupdater":  "com.google.*",
			"keystone":                  "com.google.*",
			"com.google.keystone":       "com.google.*",
			"com.google.keystone.agent": "com.google.*",
			"bravesoftware":             "com.brave.browser",
			"brave-browser":             "com.brave.browser",
			"mozilla":                   "org.mozilla.firefox",
			"firefox":                   "org.mozilla.firefox",
			"drivefs":                   "com.google.drivefs",
			"code":                      "com.microsoft.vscode",
			"slack":                     "com.tinyspeck.slackmacgap",
			"discord":                   "com.hnc.discord",
			"spotify":                   "com.spotify.client",
			"zoom.us":                   "us.zoom.xos",
			"jetbrains":                 "com.jetbrains.toolbox",
			"postman":                   "com.postmanlabs.mac",
			"figma":                     "com.figma.desktop",
			"obsidian":                  "md.obsidian",
			"notion":                    "notion.id",
			"arc":                       "company.thebrowser.browser",
			"telegram":                  "ru.keepcoder.telegram",
		},
		VendorAliases: map[string]string{
			"bravesoftware": "Brave",
			"browser":       "Brave",
		},
		IgnorePatterns: []string{
			"com.apple",
			"apple",
			"coreservices",
			"system",
			"safari",
			".ds_store",
			"askpermissiond",
			"icloudmailagent",
			"mobilesync",
			"differentialprivacy",
			"ssu",
			"callhistory",
			"desktoppictures",
			"desktop pictures",
			"addressbook",
			"knowledge",
			"syncservices",
			"fileprovider",
			"icloud",
			"icdd",
			"familycircled",
			"networkserviceproxy",
			"printingprefs",
			"sharedfilelistd",
			"mbuseragent",
			"default.store",
		},
		TokenStopwords: map[string]bool{
			"creator":   true,
			"studio":    true,
			"tools":     true,
			"desktop":   true,
			"viewer":    true,
			"utilities": true,
		},
		StaleDays:    DefaultStaleDays,
		DevStaleDays: DefaultDevStaleDays,
	}
}
