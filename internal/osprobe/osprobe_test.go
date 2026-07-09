package osprobe

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/BadRat-in/fs-janitor/internal/appindex"
	"github.com/BadRat-in/fs-janitor/internal/config"
	"github.com/BadRat-in/fs-janitor/internal/detect"
	"github.com/BadRat-in/fs-janitor/internal/scan"
)

// writeFile creates a file with n bytes so `du` reports a non-zero size.
func writeFile(t *testing.T, path string, n int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, n), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestProbesEndToEnd drives the real osprobe implementation (du, find, stat via
// the OS) through the scanner against a temp directory tree, verifying that a
// stale unattributed folder is flagged while a fresh one is skipped and sizes
// are measured. This is the integration counterpart to scan's synthetic-FS unit
// tests: it proves the live probes wire up correctly on macOS.
func TestProbesEndToEnd(t *testing.T) {
	root := t.TempDir()
	appSupport := filepath.Join(root, "Library", "Application Support")

	stale := filepath.Join(appSupport, "OldVendorLeftover")
	fresh := filepath.Join(appSupport, "FreshVendor")
	writeFile(t, filepath.Join(stale, "data.bin"), 8192)
	writeFile(t, filepath.Join(fresh, "data.bin"), 8192)

	// Age the stale dir (and its file) well past the 90-day threshold.
	old := time.Now().AddDate(0, 0, -200)
	for _, p := range []string{filepath.Join(stale, "data.bin"), stale} {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}

	cfg := config.Default(root)
	cfg.ScanDirs = []string{appSupport}
	cfg.CacheDirs = nil
	cfg.DevCacheDirs = nil
	cfg.DevToolchainDirs = nil

	engine := detect.New(appindex.New(cfg.TokenStopwords), cfg)
	res := scan.New(cfg, engine, scan.NewOptions(), New()).Scan()

	got := map[string]int64{}
	for _, g := range res.Groups {
		got[g.Vendor] = g.SizeKB
	}
	// Vendor names preserve existing capitalization (titleCaseWords only
	// upper-cases the first letter), so "OldVendorLeftover" stays as-is.
	if got["OldVendorLeftover"] == 0 {
		t.Errorf("stale unattributed folder should be flagged with non-zero size; groups=%v", got)
	}
	if _, ok := got["FreshVendor"]; ok {
		t.Errorf("fresh folder must be skipped by the staleness gate; groups=%v", got)
	}
}

// TestSizeAndRecency exercises sizeKB and recentlyWritten directly against real
// files.
func TestSizeAndRecency(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "f.bin"), 4096)

	if kb := sizeKB(dir); kb <= 0 {
		t.Errorf("sizeKB(%s) = %d, want > 0", dir, kb)
	}
	if !recentlyWritten(dir, 30) {
		t.Error("a just-written file should count as recently written within 30 days")
	}

	// Age the file beyond the window and re-check.
	old := time.Now().AddDate(0, 0, -60)
	_ = os.Chtimes(filepath.Join(dir, "f.bin"), old, old)
	if recentlyWritten(dir, 30) {
		t.Error("a 60-day-old file should NOT count as written within 30 days")
	}
}
