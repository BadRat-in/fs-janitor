package launchd

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// validConfig returns a Config that passes Plist's validation, for reuse across
// tests that only care about a subset of behaviour.
func validConfig() Config {
	return Config{
		Label:           Label,
		BinaryPath:      "/usr/local/bin/fsj",
		Args:            []string{"run"},
		IntervalSeconds: 86400,
	}
}

// TestPlistValid verifies a well-formed Config renders a plist containing the
// label, the binary and its args inside ProgramArguments, the StartInterval key
// with the configured integer, and RunAtLoad set true — plus the required
// DOCTYPE and plist wrapper.
func TestPlistValid(t *testing.T) {
	c := validConfig()
	c.StdoutPath = "/tmp/fsj.out"
	c.StderrPath = "/tmp/fsj.err"

	out, err := Plist(c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wants := []string{
		`<?xml version="1.0" encoding="UTF-8"?>`,
		`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"`,
		`<plist version="1.0">`,
		"<key>Label</key>",
		"<string>" + Label + "</string>",
		"<key>ProgramArguments</key>",
		"<string>/usr/local/bin/fsj</string>",
		"<string>run</string>",
		"<key>StartInterval</key>",
		"<integer>86400</integer>",
		"<key>RunAtLoad</key>",
		"<true/>",
		"<key>StandardOutPath</key>",
		"<string>/tmp/fsj.out</string>",
		"<key>StandardErrorPath</key>",
		"<string>/tmp/fsj.err</string>",
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("plist missing %q\n---\n%s", w, out)
		}
	}

	// The binary must precede its argument in ProgramArguments.
	if strings.Index(out, "<string>/usr/local/bin/fsj</string>") > strings.Index(out, "<string>run</string>") {
		t.Error("BinaryPath must appear before Args in ProgramArguments")
	}
}

// TestPlistOmitsOptionalLogPaths verifies StandardOutPath/StandardErrorPath keys
// are absent when the corresponding Config fields are empty.
func TestPlistOmitsOptionalLogPaths(t *testing.T) {
	out, err := Plist(validConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, "StandardOutPath") {
		t.Error("StandardOutPath must be omitted when StdoutPath is empty")
	}
	if strings.Contains(out, "StandardErrorPath") {
		t.Error("StandardErrorPath must be omitted when StderrPath is empty")
	}
}

// TestPlistEscapesXML verifies special characters in string values are escaped
// so the plist stays well-formed.
func TestPlistEscapesXML(t *testing.T) {
	c := validConfig()
	c.BinaryPath = "/opt/a&b/fsj"
	c.Args = []string{"--filter", "x<y>z"}

	out, err := Plist(c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "/opt/a&amp;b/fsj") {
		t.Errorf("'&' not escaped:\n%s", out)
	}
	if !strings.Contains(out, "x&lt;y&gt;z") {
		t.Errorf("'<'/'>' not escaped:\n%s", out)
	}
	// The raw unescaped forms must not leak into the output.
	if strings.Contains(out, "a&b") || strings.Contains(out, "x<y>z") {
		t.Errorf("raw special characters leaked into plist:\n%s", out)
	}
}

// TestPlistErrors verifies the three invalid-Config cases each return an error
// and an empty string.
func TestPlistErrors(t *testing.T) {
	cases := map[string]Config{
		"empty label":       {Label: "", BinaryPath: "/bin/fsj", IntervalSeconds: 10},
		"empty binary":      {Label: Label, BinaryPath: "", IntervalSeconds: 10},
		"zero interval":     {Label: Label, BinaryPath: "/bin/fsj", IntervalSeconds: 0},
		"negative interval": {Label: Label, BinaryPath: "/bin/fsj", IntervalSeconds: -5},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			out, err := Plist(c)
			if err == nil {
				t.Fatalf("expected error for %s", name)
			}
			if out != "" {
				t.Errorf("expected empty output on error, got %q", out)
			}
		})
	}
}

// TestPlistPath verifies the agent path is derived from home, Library, and the
// Label constant.
func TestPlistPath(t *testing.T) {
	home := t.TempDir()
	got := PlistPath(home)
	want := home + "/Library/LaunchAgents/" + Label + ".plist"
	if got != want {
		t.Errorf("PlistPath = %q, want %q", got, want)
	}
}

// TestInstall verifies Install writes the plist to PlistPath(home) with plist
// content, and calls launchctl unload then load, in that order, on the file.
func TestInstall(t *testing.T) {
	home := t.TempDir()

	var gotPath string
	var gotData []byte
	var gotPerm os.FileMode
	write := func(path string, data []byte, perm os.FileMode) error {
		gotPath, gotData, gotPerm = path, data, perm
		return nil
	}

	var calls [][]string
	run := func(name string, args ...string) error {
		calls = append(calls, append([]string{name}, args...))
		return nil
	}

	if err := Install(home, validConfig(), write, run); err != nil {
		t.Fatalf("Install: %v", err)
	}

	if want := PlistPath(home); gotPath != want {
		t.Errorf("write path = %q, want %q", gotPath, want)
	}
	if gotPerm != 0644 {
		t.Errorf("write perm = %o, want 0644", gotPerm)
	}
	if !strings.Contains(string(gotData), "<plist version=\"1.0\">") {
		t.Errorf("written bytes do not look like a plist:\n%s", gotData)
	}

	if len(calls) != 2 {
		t.Fatalf("expected 2 launchctl calls, got %d: %v", len(calls), calls)
	}
	path := PlistPath(home)
	wantUnload := []string{"launchctl", "unload", "-w", path}
	wantLoad := []string{"launchctl", "load", "-w", path}
	if !equal(calls[0], wantUnload) {
		t.Errorf("call 0 = %v, want %v", calls[0], wantUnload)
	}
	if !equal(calls[1], wantLoad) {
		t.Errorf("call 1 = %v, want %v", calls[1], wantLoad)
	}
}

// TestInstallInvalidConfig verifies Install surfaces Plist validation errors and
// performs no write or launchctl calls.
func TestInstallInvalidConfig(t *testing.T) {
	home := t.TempDir()
	wrote := false
	ran := false
	write := func(string, []byte, os.FileMode) error { wrote = true; return nil }
	run := func(string, ...string) error { ran = true; return nil }

	if err := Install(home, Config{}, write, run); err == nil {
		t.Fatal("expected error for invalid config")
	}
	if wrote || ran {
		t.Error("Install must not write or run launchctl on invalid config")
	}
}

// TestInstallLoadErrorPropagates verifies a failing `launchctl load` is returned
// while the earlier best-effort unload error is ignored.
func TestInstallLoadErrorPropagates(t *testing.T) {
	home := t.TempDir()
	write := func(string, []byte, os.FileMode) error { return nil }
	run := func(name string, args ...string) error {
		if len(args) > 0 && args[0] == "load" {
			return errors.New("load failed")
		}
		return errors.New("not loaded") // unload error must be ignored
	}
	if err := Install(home, validConfig(), write, run); err == nil {
		t.Error("expected load error to propagate")
	}
}

// TestUninstall verifies Uninstall calls launchctl unload and removes the plist
// file at PlistPath(home).
func TestUninstall(t *testing.T) {
	home := t.TempDir()

	var calls [][]string
	run := func(name string, args ...string) error {
		calls = append(calls, append([]string{name}, args...))
		return nil
	}
	var removed string
	remove := func(path string) error { removed = path; return nil }

	if err := Uninstall(home, run, remove); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}

	path := PlistPath(home)
	if len(calls) != 1 || !equal(calls[0], []string{"launchctl", "unload", "-w", path}) {
		t.Errorf("unexpected launchctl calls: %v", calls)
	}
	if removed != path {
		t.Errorf("removed = %q, want %q", removed, path)
	}
}

// TestUninstallIgnoresUnloadError verifies a failing unload does not prevent the
// plist file from being removed.
func TestUninstallIgnoresUnloadError(t *testing.T) {
	home := t.TempDir()
	run := func(string, ...string) error { return errors.New("not loaded") }
	removed := false
	remove := func(string) error { removed = true; return nil }

	if err := Uninstall(home, run, remove); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if !removed {
		t.Error("plist file must still be removed when unload fails")
	}
}

// TestUninstallRemoveErrorPropagates verifies a failing remove is returned.
func TestUninstallRemoveErrorPropagates(t *testing.T) {
	home := t.TempDir()
	run := func(string, ...string) error { return nil }
	remove := func(string) error { return errors.New("remove failed") }
	if err := Uninstall(home, run, remove); err == nil {
		t.Error("expected remove error to propagate")
	}
}

// TestIsInstalled verifies IsInstalled reflects the presence of the plist file
// using a real file under a temp home.
func TestIsInstalled(t *testing.T) {
	home := t.TempDir()

	if IsInstalled(home, os.Stat) {
		t.Error("expected not installed before file exists")
	}

	path := PlistPath(home)
	if err := os.MkdirAll(home+"/Library/LaunchAgents", 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if !IsInstalled(home, os.Stat) {
		t.Error("expected installed after file exists")
	}
}

// equal reports whether two string slices have identical contents.
func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
