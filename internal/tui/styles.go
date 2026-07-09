// styles.go centralizes the Lip Gloss styles for the FS Janitor TUI so the look
// is defined once and stays consistent across every screen. Colours are chosen
// from the 256-colour palette for broad terminal compatibility.
package tui

import "github.com/charmbracelet/lipgloss"

var (
	// Brand / accent colours.
	colAccent = lipgloss.Color("39")  // cyan-blue — primary
	colGood   = lipgloss.Color("42")  // green — sizes, healthy
	colWarn   = lipgloss.Color("214") // amber — warnings
	colDanger = lipgloss.Color("196") // red — destructive
	colDim    = lipgloss.Color("245") // grey — secondary text
	colSel    = lipgloss.Color("205") // pink — cursor/selection
	colFg     = lipgloss.Color("252") // near-white body text

	styleTitle   = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	styleHeading = lipgloss.NewStyle().Bold(true).Foreground(colWarn)
	styleDim     = lipgloss.NewStyle().Foreground(colDim)
	styleGood    = lipgloss.NewStyle().Foreground(colGood)
	styleWarn    = lipgloss.NewStyle().Foreground(colWarn)
	styleDanger  = lipgloss.NewStyle().Bold(true).Foreground(colDanger)
	styleCursor  = lipgloss.NewStyle().Bold(true).Foreground(colSel)
	styleBody    = lipgloss.NewStyle().Foreground(colFg)

	// navItem / navActive style the left module rail.
	styleNav       = lipgloss.NewStyle().Foreground(colDim).Padding(0, 1)
	styleNavActive = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("231")).
			Background(colAccent).Padding(0, 1)

	// Panel borders for the header, nav and content regions.
	styleHeader = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder(), false, false, true, false).
			BorderForeground(colAccent).Padding(0, 1)
	styleContent = lipgloss.NewStyle().Padding(1, 2)
	styleFooter  = lipgloss.NewStyle().Foreground(colDim).
			Border(lipgloss.RoundedBorder(), true, false, false, false).
			BorderForeground(colDim).Padding(0, 1)
	styleRail = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder(), false, true, false, false).
			BorderForeground(colDim).Padding(1, 2, 1, 1)
)

// bar renders a horizontal meter of the given width filled to pct (0..1) using
// block glyphs, coloured by health (green high, amber mid, red low). Used by the
// dashboard maintenance score.
func bar(pct float64, width int, healthy bool) string {
	if width < 4 {
		width = 4
	}
	if pct < 0 {
		pct = 0
	}
	if pct > 1 {
		pct = 1
	}
	filled := int(pct*float64(width) + 0.5)
	col := colGood
	switch {
	case !healthy:
		col = colDanger
	case pct < 0.5:
		col = colDanger
	case pct < 0.8:
		col = colWarn
	}
	on := lipgloss.NewStyle().Foreground(col)
	off := lipgloss.NewStyle().Foreground(colDim)
	out := ""
	for i := 0; i < width; i++ {
		if i < filled {
			out += on.Render("█")
		} else {
			out += off.Render("░")
		}
	}
	return out
}
