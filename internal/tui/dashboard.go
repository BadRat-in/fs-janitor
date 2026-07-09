// dashboard.go renders the Dashboard module: the machine's Maintenance Score,
// a health breakdown by category, and the headline lifetime/active-jobs stats.
// It is the screen the PRD envisions users opening periodically to see overall
// health and decide what to optimize next.
package tui

import (
	"strings"

	"github.com/BadRat-in/fs-janitor/internal/humanize"
	tea "github.com/charmbracelet/bubbletea"
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
	b.WriteString(styleTitle.Render("Storage Health") + "\n\n")

	if m.report == nil || m.loadingScore {
		b.WriteString(m.spinner.View() + styleDim.Render(" Scanning your machine…") + "\n")
		return b.String()
	}
	r := m.report

	// Big score line + meter.
	scoreStyle := styleGood
	if r.Score < 75 {
		scoreStyle = styleWarn
	}
	if r.Score < 40 {
		scoreStyle = styleDanger
	}
	b.WriteString(scoreStyle.Render(itoa(r.Score)+" / 100") + "  " +
		styleDim.Render("grade ") + scoreStyle.Render(r.Grade) + "\n")
	b.WriteString(bar(float64(r.Score)/100, minInt(w-8, 40), r.Score >= 40) + "\n\n")

	// Category breakdown.
	for _, c := range r.Categories {
		name := padRight(c.Name, 16)
		status := styleGood.Render("✓ " + c.Status)
		if c.Warn {
			status = styleWarn.Render("⚠ " + c.Status)
		} else if c.Status != "Clean" {
			status = styleDim.Render(c.Status)
		}
		line := "  " + styleBody.Render(name) + status
		if c.Detail != "" {
			line += styleDim.Render("  — " + c.Detail)
		}
		b.WriteString(line + "\n")
	}

	// Footer stats.
	b.WriteString("\n" + styleHeading.Render("Potential recovery ") +
		styleGood.Render(humanize.Size(r.PotentialKB)) + "\n")
	b.WriteString(styleDim.Render("Reclaimed all-time  ") +
		styleBody.Render(humanize.Size(r.LifetimeFreedKB)) + "\n")
	b.WriteString(styleDim.Render("Active jobs         ") +
		styleBody.Render(itoa(r.ActiveJobs)) + "\n")
	return b.String()
}

// minInt returns the smaller of two ints.
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
