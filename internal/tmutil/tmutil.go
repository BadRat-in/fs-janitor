// Package tmutil lists and deletes local Time Machine snapshots via the macOS
// `tmutil` command. Local snapshots are managed by macOS and safe to delete —
// it recreates them automatically — but they can account for 10-20 GB of the
// "System Data" figure in Storage settings, so CleanX offers to purge them
// after a run. This mirrors the show_tm_snapshots step of the reference script.
//
// Command execution is injected (Runner) so parsing and deletion are unit-tested
// without invoking tmutil.
package tmutil

import (
	"os/exec"
	"strings"
)

// Runner executes a command and returns its combined stdout, or an error.
type Runner func(name string, args ...string) (string, error)

// OSRunner is the production Runner; it captures stdout.
func OSRunner(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	return string(out), err
}

// SudoRunner runs a command via sudo (needed for deletelocalsnapshots).
func SudoRunner(name string, args ...string) (string, error) {
	full := append([]string{name}, args...)
	out, err := exec.Command("/usr/bin/sudo", full...).Output()
	return string(out), err
}

// ListSnapshots returns the local snapshot identifiers for the root volume.
// tmutil prints a "Snapshots for disk /:" header line that is filtered out —
// only real "com.apple.TimeMachine.*" identifiers are returned.
func ListSnapshots(run Runner) ([]string, error) {
	out, err := run("/usr/bin/tmutil", "listlocalsnapshots", "/")
	if err != nil {
		return nil, err
	}
	var snaps []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "com.apple.TimeMachine") {
			snaps = append(snaps, line)
		}
	}
	return snaps, nil
}

// DeleteSnapshot deletes one local snapshot by its identifier (requires sudo, so
// pass SudoRunner in production).
func DeleteSnapshot(run Runner, id string) error {
	_, err := run("/usr/bin/tmutil", "deletelocalsnapshots", id)
	return err
}
