// styles.go centralizes the Lip Gloss styling for the FS Janitor TUI so the
// look is defined once and stays cohesive across every screen: one palette, one
// set of panel/row/heading styles, and a couple of shared render helpers (the
// score meter and key hints). Colours come from the 256-colour palette for
// broad terminal compatibility.
package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Palette. A restrained set: one cyan accent, one pink selection, plus the
// semantic good/warn/danger trio and two greys for body/secondary text.
var (
	colAccent = lipgloss.Color("45")  // bright cyan — brand / active
	colAccDim = lipgloss.Color("31")  // muted cyan — borders, rules
	colGood   = lipgloss.Color("84")  // green — sizes, healthy
	colWarn   = lipgloss.Color("214") // amber — warnings
	colDanger = lipgloss.Color("203") // red — destructive
	colSel    = lipgloss.Color("212") // pink — cursor / selection
	colDim    = lipgloss.Color("245") // grey — secondary text
	colFaint  = lipgloss.Color("240") // darker grey — rules, disabled
	colFg     = lipgloss.Color("252") // near-white — body text
	colInk    = lipgloss.Color("233") // dark — text on accent fills
)

var (
	styleTitle   = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	styleHeading = lipgloss.NewStyle().Bold(true).Foreground(colWarn)
	styleDim     = lipgloss.NewStyle().Foreground(colDim)
	styleFaint   = lipgloss.NewStyle().Foreground(colFaint)
	styleGood    = lipgloss.NewStyle().Foreground(colGood)
	styleWarn    = lipgloss.NewStyle().Foreground(colWarn)
	styleDanger  = lipgloss.NewStyle().Bold(true).Foreground(colDanger)
	styleCursor  = lipgloss.NewStyle().Bold(true).Foreground(colSel)
	styleBody    = lipgloss.NewStyle().Foreground(colFg)

	// rowSelected highlights the row under the cursor across the content width.
	rowSelected = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("231")).
			Background(colAccDim)

	// Module rail entries. Active adds a pink leading bar and an accent fill.
	styleNav       = lipgloss.NewStyle().Foreground(colDim)
	styleNavActive = lipgloss.NewStyle().Bold(true).Foreground(colInk).Background(colAccent)

	// Chrome: header (bottom rule), the bordered content panel, footer (top rule).
	styleHeader = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), false, false, true, false).
			BorderForeground(colAccDim).Padding(0, 1)
	stylePanel = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).BorderForeground(colFaint).Padding(0, 1)
	styleFooter = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), true, false, false, false).
			BorderForeground(colFaint).Padding(0, 1)
	styleRail = lipgloss.NewStyle().Padding(1, 2, 0, 1)

	// A pink score/grade badge and a neutral pill for the header.
	styleBadge = lipgloss.NewStyle().Bold(true).Padding(0, 1)
)

// navIcons parallel navLabels: a single-width glyph per module.
var navIcons = []string{"◆", "✦", "◇", "≡", "⚙"}

// panelTitle renders the small caption shown at the top of a content panel.
func panelTitle(s string) string {
	return lipgloss.NewStyle().Bold(true).Foreground(colAccent).Render(s)
}

// keyHint renders a "key action" pair for the footer legend.
func keyHint(key, action string) string {
	k := lipgloss.NewStyle().Foreground(colAccent).Bold(true).Render(key)
	return k + " " + lipgloss.NewStyle().Foreground(colDim).Render(action)
}

// keyLegend joins several key hints with a thin separator.
func keyLegend(pairs ...[2]string) string {
	parts := make([]string, 0, len(pairs))
	for _, p := range pairs {
		parts = append(parts, keyHint(p[0], p[1]))
	}
	sep := lipgloss.NewStyle().Foreground(colFaint).Render("  ·  ")
	return strings.Join(parts, sep)
}

// badge renders a filled, coloured pill (used for the header score/grade).
func badge(text string, fg lipgloss.Color) string {
	return styleBadge.Foreground(colInk).Background(fg).Render(text)
}

// scoreColor maps a maintenance score to its semantic colour.
func scoreColor(score int) lipgloss.Color {
	switch {
	case score >= 75:
		return colGood
	case score >= 40:
		return colWarn
	default:
		return colDanger
	}
}

// bar renders a horizontal meter of the given width filled to pct (0..1) using
// block glyphs, coloured by health. Used by the dashboard score meter.
func bar(pct float64, width int, col lipgloss.Color) string {
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
	on := lipgloss.NewStyle().Foreground(col)
	off := lipgloss.NewStyle().Foreground(colFaint)
	var b strings.Builder
	for i := 0; i < width; i++ {
		if i < filled {
			b.WriteString(on.Render("█"))
		} else {
			b.WriteString(off.Render("░"))
		}
	}
	return b.String()
}
