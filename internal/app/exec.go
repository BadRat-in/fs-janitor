// exec.go holds the tiny process helper the service layer uses to run
// tool-native cleanups. Their stdout/stderr are discarded so a cleanup command
// can never corrupt the full-screen TUI; space reclaimed is measured via du,
// not parsed from the command's output.
package app

import "os/exec"

// quietRun runs an external command with its output discarded, returning only
// success/failure.
func quietRun(name string, args ...string) error {
	return exec.Command(name, args...).Run()
}
