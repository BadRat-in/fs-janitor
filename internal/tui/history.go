// history.go renders the History module: a reverse-chronological log of every
// job run and cleanup, with files removed and space reclaimed. It is the
// audit trail behind the analytics numbers ("understand what was cleaned and
// why").
package tui

import (
	"strings"

	"github.com/BadRat-in/fs-janitor/internal/humanize"
	tea "github.com/charmbracelet/bubbletea"
)

// keyHistory scrolls the run log.
func (m Model) keyHistory(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.histOffset > 0 {
			m.histOffset--
		}
	case "down", "j":
		if m.histOffset < maxInt(0, len(m.runs)-1) {
			m.histOffset++
		}
	}
	return m, nil
}

// viewHistory renders the visible window of run records.
func (m Model) viewHistory(w int) string {
	var b strings.Builder
	b.WriteString(panelTitle("History") + "\n\n")
	if len(m.runs) == 0 {
		b.WriteString(styleDim.Render("No runs recorded yet.") + "\n")
		return b.String()
	}
	rows := maxInt(4, m.height-10)
	end := minInt(len(m.runs), m.histOffset+rows)
	for _, r := range m.runs[m.histOffset:end] {
		when := r.RanAt.Format("01-02 15:04")
		kind := padRight(string(r.Kind), 7)
		freed := styleGood.Render(padRight(humanize.Size(r.FreedKB), 8))
		note := r.Note
		if r.DryRun {
			note = "(dry) " + note
		}
		tail := styleDim.Render(truncate(r.Target+"  "+note, maxInt(20, w-30)))
		b.WriteString(styleDim.Render(when) + "  " + styleHeading.Render(kind) + freed + tail + "\n")
	}
	if len(m.runs) > rows {
		b.WriteString("\n" + styleDim.Render("showing "+itoa(m.histOffset+1)+"–"+itoa(end)+" of "+itoa(len(m.runs))) + "\n")
	}
	return b.String()
}
