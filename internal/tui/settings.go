// settings.go renders the Settings module: cleanup preferences plus the
// Automation toggle that installs/removes the LaunchAgent so `fsj run` executes
// on a schedule. Every TUI action has a CLI equivalent (package cli); this
// screen is the interactive front for the same operations.
package tui

import (
	"os"
	"strings"

	"github.com/BadRat-in/fs-janitor/internal/launchd"
	tea "github.com/charmbracelet/bubbletea"
)

// settingLabels are the rows on the settings screen, in order.
var settingLabels = []string{
	"Sort cleanup by size",
	"Include toolchains in cleanup",
	"Automation — run hourly (LaunchAgent)",
}

const (
	setBySize = iota
	setToolchains
	setAutomation
)

// keySettings handles Settings navigation and toggles.
func (m Model) keySettings(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.setCursor > 0 {
			m.setCursor--
		}
	case "down", "j":
		if m.setCursor < len(settingLabels)-1 {
			m.setCursor++
		}
	case " ", "enter":
		return m.applySetting()
	}
	return m, nil
}

// applySetting toggles the focused setting, performing the automation
// install/uninstall as a side effect when that row is chosen.
func (m Model) applySetting() (tea.Model, tea.Cmd) {
	switch m.setCursor {
	case setBySize:
		m.bySize = !m.bySize
	case setToolchains:
		m.includeToolchains = !m.includeToolchains
	case setAutomation:
		if m.autoInstalled {
			if err := launchd.UninstallDefault(m.app.Home); err != nil {
				m.status = "Automation removal failed: " + err.Error()
			} else {
				m.autoInstalled = false
				m.status = "Automation removed."
			}
			return m, nil
		}
		if err := m.installAutomation(); err != nil {
			m.status = "Automation install failed: " + err.Error()
		} else {
			m.autoInstalled = true
			m.status = "Automation installed — cleanup runs hourly."
		}
	}
	return m, nil
}

// installAutomation installs the LaunchAgent for the current executable so it
// runs `fsj run` every hour, logging to the Application Support data dir.
func (m Model) installAutomation() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	logDir := m.app.Home + "/Library/Application Support/fs-janitor"
	return launchd.InstallDefault(m.app.Home, launchd.Config{
		Label:           launchd.Label,
		BinaryPath:      exe,
		Args:            []string{"run"},
		IntervalSeconds: 3600,
		StdoutPath:      logDir + "/fsj.out.log",
		StderrPath:      logDir + "/fsj.err.log",
	})
}

// viewSettings renders the settings rows with their current values.
func (m Model) viewSettings(w int) string {
	var b strings.Builder
	b.WriteString(panelTitle("Settings") + "\n\n")
	values := []string{
		onOff(m.bySize),
		onOff(m.includeToolchains),
		installedLabel(m.autoInstalled),
	}
	for i, label := range settingLabels {
		cursor := "  "
		if i == m.setCursor {
			cursor = styleCursor.Render("▸ ")
		}
		b.WriteString(cursor + styleBody.Render(padRight(label, 40)) + values[i] + "\n")
	}
	b.WriteString("\n" + styleDim.Render("Cleanup deletes caches permanently; job actions default to Trash (recoverable).") + "\n")
	return b.String()
}

// onOff renders a boolean setting value.
func onOff(v bool) string {
	if v {
		return styleGood.Render("ON")
	}
	return styleDim.Render("OFF")
}

// installedLabel renders the automation install state.
func installedLabel(v bool) string {
	if v {
		return styleGood.Render("INSTALLED")
	}
	return styleDim.Render("not installed")
}
