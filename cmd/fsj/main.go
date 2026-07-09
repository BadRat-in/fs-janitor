// Command fsj is FS Janitor: the filesystem maintenance toolkit for macOS.
//
// It combines storage cleanup (leftover apps, developer/build caches, Time
// Machine snapshots), lifecycle jobs (one-time expirations), directory watchers
// (recurring cleanup policies), scheduling via LaunchAgent, and a maintenance
// score — behind both a scriptable CLI and a full-screen Bubble Tea TUI.
//
// Run `fsj` with no arguments to open the interactive dashboard; run
// `fsj <command>` (expire, watch, list, run, clean, score, history, install,
// doctor, …) to drive the same operations from scripts and the scheduler. See
// `fsj --help`.
package main

import (
	"os"

	"github.com/BadRat-in/fs-janitor/internal/cli"
)

// version is overwritten at build time via
// -ldflags "-X main.version=vX.Y.Z" by the release workflow.
var version = "dev"

func main() {
	os.Exit(cli.Execute(version))
}
