// Package scan walks the configured Library and developer directories, applies
// CleanX's safety gates (installed / in-use / staleness, via package detect),
// and groups the surviving leftovers by vendor for presentation and deletion.
//
// This file holds vendor-name extraction: turning a filesystem path into the
// human-readable group label shown to the user. It is a direct port of the
// get_vendor_name function in the reference CleanX.zsh and is unit-tested
// against that function's documented examples.
package scan

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/BadRat-in/fs-janitor/internal/config"
)

// trailingDigits strips a run of digits at the end of a token, so
// "Sublime_Text3" -> "Sublime_Text". Mirrors the sed 's/[0-9]+$//' step.
var trailingDigits = regexp.MustCompile(`[0-9]+$`)

// wrapperExts are the suffixes stripped from a bundle component before vendor
// extraction (same set package detect strips).
var wrapperExts = []string{".plist", ".savedState", ".binarycookies"}

// GetVendorName derives the display vendor/group name for an item at path that
// lives under scanRoot. The depth-1 component under scanRoot is treated as the
// bundle component (e.g. "com.spotify.client"); its vendor token is extracted
// and title-cased. Examples (from the reference script):
//
//	com.spotify.client              -> "Spotify"
//	org.mozilla.firefox             -> "Mozilla"
//	pro.betterdisplay.BetterDisplay -> "Betterdisplay"
//	io.zed.zed                      -> "Zed"
//	com.nuebling.mac-mouse-fix      -> "Mac Mouse Fix"
//	com.brave.Browser               -> "Brave" (via alias)
//	Kitty                           -> "Kitty" (plain dir name)
//
// aliases canonicalises vendor spellings (config.Config.VendorAliases). An empty
// path yields "Unknown".
func GetVendorName(path, scanRoot string, aliases map[string]string) string {
	if path == "" {
		return "Unknown"
	}

	// Depth-1 component under scanRoot (or basename when scanRoot is empty).
	var bundleComponent string
	if scanRoot != "" {
		rel := strings.TrimPrefix(path, scanRoot+string(filepath.Separator))
		bundleComponent = strings.SplitN(rel, string(filepath.Separator), 2)[0]
	} else {
		bundleComponent = filepath.Base(path)
	}

	base := bundleComponent
	for _, ext := range wrapperExts {
		base = strings.TrimSuffix(base, ext)
	}

	var token string
	if config.BundleIDRegex.MatchString(strings.ToLower(base)) {
		// Strip the TLD segment (everything up to and including the first dot).
		noTLD := base
		if i := strings.Index(base, "."); i >= 0 {
			noTLD = base[i+1:]
		}
		segs := strings.Split(noTLD, ".")
		vendorSeg := segs[0]
		appSeg := ""
		if len(segs) > 1 {
			appSeg = segs[1]
		}
		// Prefer the app-name segment when it is longer (more descriptive):
		// "nuebling.mac-mouse-fix" -> "mac-mouse-fix" beats "nuebling".
		if appSeg != "" && len(appSeg) > len(vendorSeg) {
			token = appSeg
		} else {
			token = vendorSeg
		}
	} else {
		token = base
	}

	token = trailingDigits.ReplaceAllString(token, "")

	// Canonical alias (BraveSoftware/Browser -> Brave) before title-casing.
	if alias, ok := aliases[strings.ToLower(token)]; ok {
		return alias
	}

	vendor := titleCaseWords(strings.NewReplacer("-", " ", "_", " ").Replace(token))
	if vendor == "" {
		return "Unknown"
	}
	return vendor
}

// titleCaseWords upper-cases the first letter of each whitespace-separated word,
// leaving the rest of each word untouched — matching the awk step in the script
// (which does not lowercase the tail, so "BetterDisplay" stays "BetterDisplay").
func titleCaseWords(s string) string {
	fields := strings.Fields(s)
	for i, w := range fields {
		if w == "" {
			continue
		}
		r := []rune(w)
		r[0] = []rune(strings.ToUpper(string(r[0])))[0]
		fields[i] = string(r)
	}
	return strings.Join(fields, " ")
}
