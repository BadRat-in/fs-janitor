// Package detect implements CleanX's leftover-attribution logic: given a
// filesystem component name (a folder, a plist, a bundle-ID directory) it
// decides whether that component belongs to a currently installed app, and
// whether it is a *confirmed leftover* of an uninstalled one.
//
// This is the safety-critical core of the tool — a false "not installed" here
// is what leads to deleting a live app's data — so it is a careful, line-for-
// line port of the `component_is_installed` / `component_is_confirmed_leftover`
// functions in the reference CleanX.zsh, kept deliberately explicit and fully
// unit-tested. Every ambiguous match resolves toward *protection*.
//
// The engine is stateless w.r.t. the filesystem: it operates purely on an
// appindex.Index (what is installed) plus static config.Config data (hints,
// aliases, stopwords). Results are memoized per component for the lifetime of
// the engine.
package detect

import (
	"strings"

	"github.com/BadRat-in/fs-janitor/internal/appindex"
	"github.com/BadRat-in/fs-janitor/internal/config"
)

// Engine answers installed / leftover questions against a fixed index + config.
// Construct one per scan with New; it is not safe for concurrent use (the memo
// cache is a plain map).
type Engine struct {
	ix    *appindex.Index
	cfg   *config.Config
	cache map[string]bool // memo: normalized component -> installed?
}

// New returns an Engine backed by the given installed-app index and config.
func New(ix *appindex.Index, cfg *config.Config) *Engine {
	return &Engine{ix: ix, cfg: cfg, cache: map[string]bool{}}
}

// normalizeComponent lowercases a raw component name and strips the wrapper
// extensions that enclose a bundle ID (.plist, .savedState, .binarycookies),
// so "com.foo.bar.plist" keys as "com.foo.bar". Mirrors normalize_component.
func normalizeComponent(comp string) string {
	c := strings.ToLower(comp)
	for _, ext := range []string{".plist", ".savedstate", ".binarycookies"} {
		c = strings.TrimSuffix(c, ext)
	}
	return c
}

// isBundleID reports whether a normalized component looks like a reverse-domain
// bundle-ID name (matches the shared TLD-prefix regex).
func (e *Engine) isBundleID(comp string) bool {
	return config.BundleIDRegex.MatchString(comp)
}

// ComponentInstalled reports whether the app owning this component is currently
// installed. Resolution order (first hit wins), matching the reference script:
//
//  1. explicit hint map (PlainDirAppHints), including "prefix*" wildcards for
//     vendor-shared components,
//  2. for bundle-ID components: exact/prefix bundle-ID match, then name-segment
//     match, then app-name-token containment on the *app* segments only,
//  3. for plain names: exact/normalized name match, then bidirectional
//     containment (>= 4 chars) against app names, then app-name tokens.
//
// Containment matches only ever protect (return true), so a false positive
// errs on the safe side. The result is memoized.
func (e *Engine) ComponentInstalled(comp string) bool {
	comp = normalizeComponent(comp)
	if v, ok := e.cache[comp]; ok {
		return v
	}
	hit := e.compute(comp)
	e.cache[comp] = hit
	return hit
}

func (e *Engine) compute(comp string) bool {
	// (1) Explicit hint mapping.
	if hint, ok := e.cfg.PlainDirAppHints[comp]; ok {
		if strings.HasSuffix(hint, "*") {
			prefix := strings.TrimSuffix(hint, "*")
			for bid := range e.ix.BundleIDs {
				if strings.HasPrefix(bid, prefix) {
					return true
				}
			}
			return false
		}
		return e.ix.BundleIDs[hint]
	}

	// (2) Reverse-domain bundle-ID component.
	if e.isBundleID(comp) {
		if e.ix.BundleIDs[comp] {
			return true
		}
		// Prefix relation in either direction:
		// com.spotify.client.helper <-> com.spotify.client.
		for bid := range e.ix.BundleIDs {
			if strings.HasPrefix(comp, bid+".") || strings.HasPrefix(bid, comp+".") {
				return true
			}
		}
		// Name segments after the TLD: com.spotify.client -> spotify, client.
		segs := strings.Split(comp, ".")
		for _, seg := range segs[1:] {
			if e.ix.AppNames[seg] {
				return true
			}
		}
		// App-name tokens inside the app segments only (skip TLD and vendor):
		// us.zoom.ZoomDaemon -> "zoomdaemon" contains "zoom". The vendor segment
		// is excluded so com.google.chrome is NOT protected merely because
		// Google Drive is installed.
		if len(segs) > 2 {
			for _, seg := range segs[2:] {
				for tok := range e.ix.NameTokens {
					if strings.Contains(seg, tok) {
						return true
					}
				}
			}
		}
		return false
	}

	// (3) Plain folder / plist name.
	norm := strings.NewReplacer(" ", "", "-", "", "_", "").Replace(comp)
	if e.ix.AppNames[comp] || e.ix.AppNames[norm] {
		return true
	}
	if len(norm) >= 4 {
		// Containment: "code" is inside "vscode"/"xcode" -> protect.
		for nm := range e.ix.AppNames {
			if strings.Contains(nm, norm) {
				return true
			}
		}
		// Reverse: an installed app's name word inside the component ties
		// "zoomdaemon"/"zoomclips" back to zoom.us.
		for tok := range e.ix.NameTokens {
			if strings.Contains(norm, tok) {
				return true
			}
		}
	}
	return false
}

// ComponentConfirmedLeftover reports whether a component can be positively
// attributed to an app (a reverse-domain bundle ID, or an entry in the hint
// map) that is NOT installed. Confirmed leftovers bypass the staleness age gate
// — a freshly uninstalled app's files are leftovers immediately. Components we
// cannot attribute return false (they must fall back to the staleness rule).
func (e *Engine) ComponentConfirmedLeftover(comp string) bool {
	comp = normalizeComponent(comp)
	_, hinted := e.cfg.PlainDirAppHints[comp]
	if hinted || e.isBundleID(comp) {
		return !e.ComponentInstalled(comp)
	}
	return false
}
