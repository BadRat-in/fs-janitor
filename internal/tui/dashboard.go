// dashboard.go renders the Dashboard module: the machine's Maintenance Score,
// a health breakdown by category, and the headline lifetime/active-jobs stats.
// It is the screen the PRD envisions users opening periodically to see overall
// health and decide what to optimize next.
package tui

import (
	"strings"

	"github.com/BadRat-in/fs-janitor/internal/humanize"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// keyDashboard handles Dashboard keys: 'r' triggers a rescan/recompute.
func (m Model) keyDashboard(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "r":
		m.loadingScore = true
		m.report = nil
		return m, tea.Batch(m.spinner.Tick, m.loadScoreCmd())
	}
	return m, nil
}

// viewDashboard renders the score, meter and category breakdown.
func (m Model) viewDashboard(w int) string {
	var b strings.Builder
	b.WriteString(panelTitle("Storage Health") + "\n\n")

	if m.report == nil || m.loadingScore {
		b.WriteString(m.spinner.View() + styleDim.Render(" Scanning your machine…") + "\n")
		return b.String()
	}
	r := m.report

	col := scoreColor(r.Score)
	scoreStyle := lipgloss.NewStyle().Bold(true).Foreground(col)

	// Big score + meter.
	b.WriteString(scoreStyle.Render(itoa(r.Score)+" / 100") +
		styleDim.Render("   grade ") + badge(" "+r.Grade+" ", col) + "\n")
	b.WriteString(bar(float64(r.Score)/100, minInt(w-6, 44), col) + "\n\n")

	// Category breakdown, aligned.
	for _, c := range r.Categories {
		name := padRight(c.Name, 16)
		var status string
		switch {
		case c.Warn:
			status = styleWarn.Render("⚠ " + padRight(c.Status, 8))
		case c.Status == "Clean":
			status = styleGood.Render("✓ " + padRight("Clean", 8))
		default:
			status = styleDim.Render("  " + padRight(c.Status, 8))
		}
		line := "  " + styleBody.Render(name) + status
		if c.Detail != "" {
			line += styleFaint.Render("  " + c.Detail)
		}
		b.WriteString(line + "\n")
	}

	// Headline stats.
	b.WriteString("\n")
	b.WriteString(stat("Potential recovery", styleGood.Render(humanize.Size(r.PotentialKB))))
	b.WriteString(stat("Reclaimed all-time", styleBody.Render(humanize.Size(r.LifetimeFreedKB))))
	b.WriteString(stat("Active jobs", styleBody.Render(itoa(r.ActiveJobs))))
	b.WriteString("\n" + styleFaint.Render("press r to rescan"))
	return b.String()
}

// stat renders one aligned "label  value" line for the dashboard footer.
func stat(label, value string) string {
	return styleDim.Render(padRight(label, 20)) + value + "\n"
}

// minInt returns the smaller of two ints.
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
