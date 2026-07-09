// Package launchd installs, uninstalls, and renders the macOS LaunchAgent that
// drives fs-janitor's periodic cleanup (the PRD's "Automation" module). When the
// user opts into scheduled maintenance, fs-janitor writes a per-user LaunchAgent
// property list to ~/Library/LaunchAgents and registers it with launchctl so the
// system runs `fsj run` on a recurring schedule without any daemon of our own.
//
// # Scheduling model: StartInterval + RunAtLoad
//
// Laptops sleep and power off, so a wall-clock schedule (StartCalendarInterval)
// would silently skip every fire that lands while the machine is asleep. We use
// StartInterval instead: launchd tracks elapsed time and, if one or more fires
// were missed while the machine was unavailable, it runs the job once at the next
// wake. RunAtLoad additionally kicks off a run the moment the agent is loaded
// (install time and login), so cleanup happens promptly rather than only after a
// full interval has elapsed. This trades exact-time precision (which is
// meaningless for a cleanup chore) for the guarantee that maintenance actually
// happens on machines that are rarely awake at any fixed hour.
//
// # Injected seams
//
// The plist body is produced by Plist, a pure function with no I/O, so its exact
// XML can be asserted in unit tests. Install/Uninstall/IsInstalled take function
// seams for the side-effecting operations — file writes, file removal, stat, and
// launchctl invocation — so tests exercise the full control flow (path
// computation, call ordering, error handling) without touching the real
// filesystem or spawning launchctl. Production callers use the *Default wrappers,
// which bind those seams to os.WriteFile, os.Remove, os.Stat, and OSRunner.
package launchd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Label is the launchd job label for fs-janitor's cleanup agent. It doubles as
// the plist file's basename (Label + ".plist") and as the identifier passed to
// launchctl, so it must be a stable, reverse-DNS string that is unique among the
// user's LaunchAgents.
const Label = "com.badrat.fsj"

// Config describes the LaunchAgent to render and install.
//
// Fields:
//   - Label: the launchd job label (normally the package-level Label constant).
//     Required; must be non-empty.
//   - BinaryPath: absolute path to the fsj executable to launch. Required; must
//     be non-empty. It becomes the first element of ProgramArguments.
//   - Args: arguments passed to BinaryPath (for example {"run"}). May be nil or
//     empty; each element becomes a subsequent ProgramArguments entry.
//   - IntervalSeconds: the StartInterval, i.e. how often launchd should run the
//     job, in seconds. Required; must be > 0.
//   - StdoutPath: file to which the job's stdout is redirected. Optional; when
//     empty no StandardOutPath key is emitted.
//   - StderrPath: file to which the job's stderr is redirected. Optional; when
//     empty no StandardErrorPath key is emitted.
type Config struct {
	Label           string
	BinaryPath      string
	Args            []string
	IntervalSeconds int
	StdoutPath      string
	StderrPath      string
}

// Runner executes a command by name and arguments, returning only an error. It
// abstracts launchctl invocation so Install/Uninstall can be unit-tested by
// substituting a fake that records calls instead of spawning a process.
type Runner func(name string, args ...string) error

// OSRunner is the production Runner. It runs the command, wiring the child's
// stdout and stderr to the parent's so launchctl diagnostics are visible, and
// returns the process exit error (if any).
func OSRunner(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// PlistPath returns the absolute path of the LaunchAgent plist for the given
// home directory: <home>/Library/LaunchAgents/<Label>.plist. Taking home as a
// parameter (rather than reading it from the environment) keeps the function
// pure and lets tests point it at a t.TempDir().
func PlistPath(home string) string {
	return filepath.Join(home, "Library", "LaunchAgents", Label+".plist")
}

// escapeXML replaces the five characters that are significant in XML character
// data / attribute values with their entity references, so arbitrary path and
// argument strings can be embedded in the plist without producing malformed XML.
//
// Parameters:
//   - s: the raw string value to escape.
//
// Returns the escaped string, safe to place inside a <string> element.
func escapeXML(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		"\"", "&quot;",
		"'", "&apos;",
	)
	return r.Replace(s)
}

// Plist renders the LaunchAgent property list for c as an XML string. It is a
// pure function: it performs no I/O and its output depends only on its input,
// which makes the exact plist assertable in tests.
//
// The emitted plist contains the standard XML declaration, the Apple plist
// DOCTYPE, a <plist version="1.0"> root, and a top-level <dict> with these keys:
//   - Label: c.Label.
//   - ProgramArguments: c.BinaryPath followed by each element of c.Args.
//   - StartInterval: c.IntervalSeconds (see the package doc for the rationale).
//   - RunAtLoad: <true/>, so the job also runs immediately on load.
//   - StandardOutPath / StandardErrorPath: emitted only when the corresponding
//     Config field is non-empty.
//
// All string values are XML-escaped via escapeXML.
//
// Parameters:
//   - c: the agent configuration to render.
//
// Returns the plist XML and a nil error on success. It returns an empty string
// and a non-nil error when c.Label is empty, c.BinaryPath is empty, or
// c.IntervalSeconds <= 0 — the three conditions that would otherwise yield a
// plist launchd rejects or one that never runs.
func Plist(c Config) (string, error) {
	if c.Label == "" {
		return "", fmt.Errorf("launchd: Label must not be empty")
	}
	if c.BinaryPath == "" {
		return "", fmt.Errorf("launchd: BinaryPath must not be empty")
	}
	if c.IntervalSeconds <= 0 {
		return "", fmt.Errorf("launchd: IntervalSeconds must be > 0, got %d", c.IntervalSeconds)
	}

	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	b.WriteString(`<plist version="1.0">` + "\n")
	b.WriteString("<dict>\n")

	// Label: the job identifier launchctl and the system use to track the agent.
	b.WriteString("\t<key>Label</key>\n")
	b.WriteString("\t<string>" + escapeXML(c.Label) + "</string>\n")

	// ProgramArguments: argv for the job — binary first, then each argument.
	b.WriteString("\t<key>ProgramArguments</key>\n")
	b.WriteString("\t<array>\n")
	b.WriteString("\t\t<string>" + escapeXML(c.BinaryPath) + "</string>\n")
	for _, arg := range c.Args {
		b.WriteString("\t\t<string>" + escapeXML(arg) + "</string>\n")
	}
	b.WriteString("\t</array>\n")

	// StartInterval: run every IntervalSeconds; missed fires collapse into one
	// run at the next wake (see package doc).
	b.WriteString("\t<key>StartInterval</key>\n")
	b.WriteString(fmt.Sprintf("\t<integer>%d</integer>\n", c.IntervalSeconds))

	// RunAtLoad: also run immediately when the agent is loaded (install/login).
	b.WriteString("\t<key>RunAtLoad</key>\n")
	b.WriteString("\t<true/>\n")

	// Optional log redirection: only emitted when a path is configured.
	if c.StdoutPath != "" {
		b.WriteString("\t<key>StandardOutPath</key>\n")
		b.WriteString("\t<string>" + escapeXML(c.StdoutPath) + "</string>\n")
	}
	if c.StderrPath != "" {
		b.WriteString("\t<key>StandardErrorPath</key>\n")
		b.WriteString("\t<string>" + escapeXML(c.StderrPath) + "</string>\n")
	}

	b.WriteString("</dict>\n")
	b.WriteString("</plist>\n")
	return b.String(), nil
}

// Install renders c to a plist and registers it as a LaunchAgent under home.
//
// Steps, in order:
//  1. Render the plist via Plist (returns early on invalid Config).
//  2. Ensure <home>/Library/LaunchAgents exists (os.MkdirAll, 0755).
//  3. Write the plist to PlistPath(home) with 0644 permissions via write.
//  4. Best-effort `launchctl unload -w <path>` to clear any previously loaded
//     copy; its error is intentionally ignored because the agent is usually not
//     loaded yet, and "not loaded" is not a failure for a fresh install.
//  5. `launchctl load -w <path>` to register and enable the agent; its error is
//     returned.
//
// Parameters:
//   - home: the user's home directory; determines the plist location.
//   - c: the agent configuration to install.
//   - write: seam for writing the plist file (production: os.WriteFile).
//   - run: seam for invoking launchctl (production: OSRunner).
//
// Returns a non-nil error if the Config is invalid, the LaunchAgents directory
// cannot be created, the plist cannot be written, or the final `launchctl load`
// fails. The directory-creation step uses os.MkdirAll directly (a benign,
// idempotent operation) rather than an injected seam.
func Install(home string, c Config, write func(path string, data []byte, perm os.FileMode) error, run Runner) error {
	body, err := Plist(c)
	if err != nil {
		return err
	}

	path := PlistPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("launchd: create LaunchAgents dir: %w", err)
	}
	if err := write(path, []byte(body), 0644); err != nil {
		return fmt.Errorf("launchd: write plist: %w", err)
	}

	// Clear any stale registration first; "not loaded" is expected and ignored.
	_ = run("launchctl", "unload", "-w", path)
	if err := run("launchctl", "load", "-w", path); err != nil {
		return fmt.Errorf("launchd: launchctl load: %w", err)
	}
	return nil
}

// Uninstall unregisters the LaunchAgent under home and deletes its plist file.
//
// Steps, in order:
//  1. Best-effort `launchctl unload -w <path>`; its error is intentionally
//     ignored so an already-unloaded or never-loaded agent still gets its file
//     removed rather than aborting the uninstall.
//  2. Remove the plist file via remove; its error is returned.
//
// Parameters:
//   - home: the user's home directory; determines the plist location.
//   - run: seam for invoking launchctl (production: OSRunner).
//   - remove: seam for deleting the plist file (production: os.Remove).
//
// Returns a non-nil error only when removing the plist file fails.
func Uninstall(home string, run Runner, remove func(path string) error) error {
	path := PlistPath(home)
	// Ignore unload errors: the agent may not be loaded, which is fine.
	_ = run("launchctl", "unload", "-w", path)
	if err := remove(path); err != nil {
		return fmt.Errorf("launchd: remove plist: %w", err)
	}
	return nil
}

// IsInstalled reports whether the LaunchAgent plist exists for home, i.e.
// whether stat(PlistPath(home)) succeeds. It only checks for the file's
// presence; it does not consult launchctl for the agent's loaded state.
//
// Parameters:
//   - home: the user's home directory; determines the plist location.
//   - stat: seam for probing the file (production: os.Stat).
//
// Returns true when stat returns no error, false otherwise (including when the
// file does not exist or is otherwise inaccessible).
func IsInstalled(home string, stat func(string) (os.FileInfo, error)) bool {
	_, err := stat(PlistPath(home))
	return err == nil
}

// InstallDefault is the production convenience wrapper around Install, binding
// the write seam to os.WriteFile and the run seam to OSRunner.
//
// Parameters:
//   - home: the user's home directory.
//   - c: the agent configuration to install.
//
// Returns whatever Install returns.
func InstallDefault(home string, c Config) error {
	return Install(home, c, os.WriteFile, OSRunner)
}

// UninstallDefault is the production convenience wrapper around Uninstall,
// binding the run seam to OSRunner and the remove seam to os.Remove.
//
// Parameters:
//   - home: the user's home directory.
//
// Returns whatever Uninstall returns.
func UninstallDefault(home string) error {
	return Uninstall(home, OSRunner, os.Remove)
}
