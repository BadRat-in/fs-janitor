package appindex

import (
	"os"
	"path/filepath"
	"testing"
)

// TestAddAppSignals verifies name normalization, tokenization (length + stopword
// rules), and bundle-ID recording.
func TestAddAppSignals(t *testing.T) {
	stop := map[string]bool{"creator": true}
	ix := New(stop)
	ix.AddApp("Mac Mouse Fix", "com.nuebling.mac-mouse-fix")
	ix.AddApp("zoom.us", "us.zoom.xos")
	ix.AddApp("Keynote Creator Studio", "") // no bundle id; "creator" is a stopword

	// Raw + normalized names. Normalization strips spaces/hyphens/underscores
	// but keeps dots, so "zoom.us" stays "zoom.us" (matching the reference).
	for _, want := range []string{"mac mouse fix", "macmousefix", "zoom.us"} {
		if !ix.AppNames[want] {
			t.Errorf("AppNames missing %q", want)
		}
	}
	// Tokens: >= 4 chars, stopwords excluded.
	if !ix.NameTokens["zoom"] {
		t.Error("expected token 'zoom'")
	}
	if !ix.NameTokens["mouse"] {
		t.Error("expected token 'mouse'")
	}
	if ix.NameTokens["us"] {
		t.Error("token 'us' is too short (<4) and must be excluded")
	}
	if ix.NameTokens["fix"] {
		t.Error("token 'fix' is too short (<4) and must be excluded")
	}
	if ix.NameTokens["creator"] {
		t.Error("stopword 'creator' must not be a token")
	}
	// Bundle IDs lowercased; empty ignored.
	if !ix.BundleIDs["com.nuebling.mac-mouse-fix"] || !ix.BundleIDs["us.zoom.xos"] {
		t.Error("expected bundle IDs recorded lowercase")
	}
	if len(ix.BundleIDs) != 2 {
		t.Errorf("expected exactly 2 bundle IDs, got %d", len(ix.BundleIDs))
	}
}

// TestBuildWithFakeReader exercises Build end-to-end against a temp dir of fake
// ".app" bundles, using an injected BundleIDReader so no real plist is needed.
func TestBuildWithFakeReader(t *testing.T) {
	dir := t.TempDir()
	alpha := filepath.Join(dir, "Alpha.app")
	beta := filepath.Join(dir, "Beta.app")
	for _, p := range []string{alpha, beta} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", p, err)
		}
	}
	// A non-.app file that must be ignored by the *.app glob.
	if err := os.WriteFile(filepath.Join(dir, "NotAnApp.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	ids := map[string]string{
		alpha: "com.example.alpha",
		beta:  "com.example.beta",
	}
	reader := func(p string) string { return ids[p] }

	ix := Build([]string{filepath.Join(dir, "*.app")}, nil, reader)
	if !ix.BundleIDs["com.example.alpha"] || !ix.BundleIDs["com.example.beta"] {
		t.Error("Build did not record both bundle IDs")
	}
	if !ix.AppNames["alpha"] || !ix.AppNames["beta"] {
		t.Error("Build did not record both app names")
	}
	if len(ix.BundleIDs) != 2 {
		t.Errorf("expected 2 bundle IDs, got %d", len(ix.BundleIDs))
	}
}
