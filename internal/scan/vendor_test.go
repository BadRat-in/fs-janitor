package scan

import (
	"testing"

	"github.com/BadRat-in/fs-janitor/internal/config"
)

// TestGetVendorName covers the documented examples plus alias resolution and
// version-digit stripping, using a scan root so the depth-1 component logic is
// exercised.
func TestGetVendorName(t *testing.T) {
	aliases := config.Default("/Users/tester").VendorAliases
	root := "/Users/tester/Library/Application Support"
	cases := []struct {
		path string
		want string
	}{
		{root + "/com.spotify.client", "Spotify"},
		{root + "/org.mozilla.firefox", "Mozilla"},
		{root + "/pro.betterdisplay.BetterDisplay", "Betterdisplay"},
		{root + "/io.zed.zed", "Zed"},
		{root + "/com.nuebling.mac-mouse-fix", "Mac Mouse Fix"},
		{root + "/com.brave.Browser", "Brave"},                  // alias on app segment "Browser"
		{root + "/BraveSoftware", "Brave"},                      // alias on plain folder
		{root + "/Kitty", "Kitty"},                              // plain dir
		{root + "/com.sublimehq.Sublime_Text3", "Sublime Text"}, // digits stripped, underscores -> spaces
		{root + "/com.spotify.client.plist", "Spotify"},         // wrapper ext stripped
		{"", "Unknown"},
	}
	for _, c := range cases {
		if got := GetVendorName(c.path, root, aliases); got != c.want {
			t.Errorf("GetVendorName(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

// TestGetVendorNameBasename verifies the no-scan-root path (basename mode).
func TestGetVendorNameBasename(t *testing.T) {
	if got := GetVendorName("/some/where/Figma", "", nil); got != "Figma" {
		t.Errorf("basename mode = %q, want Figma", got)
	}
}
