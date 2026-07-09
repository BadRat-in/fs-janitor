// cleanup.go renders the Cleanup module: the reused CleanX scan surfaced as an
// interactive, sectioned multi-select inside the app shell. The user scans,
// selects leftover/cache groups (and tool-native cleanups), and cleans — with
// live size totals — without leaving the dashboard.
package tui

import (
	"sort"
	"strings"

	"github.com/BadRat-in/fs-janitor/internal/humanize"
	"github.com/BadRat-in/fs-janitor/internal/scan"
	"github.com/BadRat-in/fs-janitor/internal/toolclean"
	tea "github.com/charmbracelet/bubbletea"
)

// Cleanup section labels, in display order.
const (
	secDevCache  = "Developer / Build-Tool Caches"
	secToolchain = "Toolchains / Runtimes (destructive)"
	secLibrary   = "App Library Leftovers"
	secToolClean = "Tool-Native Cache Cleanup"
)

var cleanupSectionOrder = []string{secDevCache, secToolchain, secLibrary, secToolClean}

// scanCmd runs a cleanup scan honoring the current settings toggles.
func (m Model) scanCmd() tea.Cmd {
	a := m.app
	opts := scan.NewOptions()
	opts.IncludeToolchains = m.includeToolchains
	return func() tea.Msg {
		return scanMsg{res: a.Scan(opts), tools: a.ToolCleanups()}
	}
}

// cleanCmd deletes every selected group and runs every selected tool cleanup,
// returning the total kilobytes reclaimed.
func (m Model) cleanCmd() tea.Cmd {
	a := m.app
	sel := m.selectedCleanup()
	return func() tea.Msg {
		var freed int64
		for _, it := range sel {
			if it.tool != nil {
				if kb, err := a.RunTool(*it.tool); err == nil {
					freed += kb
				}
				continue
			}
			freed += a.DeleteCleanup(it.paths).FreedKB
		}
		return cleanDoneMsg{freedKB: freed}
	}
}

// buildCleanupItems converts scan results + tool cleanups into the selectable
// list, grouped and ordered by section then (per settings) size or name.
func (m *Model) buildCleanupItems(res scan.Result, tools []toolclean.Cleanup) {
	m.cleanupItems = nil
	m.cleanDone = false
	m.cleanCursor = 0
	for _, g := range res.Groups {
		sec, destructive := secLibrary, false
		switch g.Kind {
		case scan.KindDevCache:
			sec = secDevCache
		case scan.KindToolchain:
			sec, destructive = secToolchain, true
		}
		paths := append([]string{}, g.Paths...)
		m.cleanupItems = append(m.cleanupItems, &litem{
			label: g.Vendor, sizeKB: g.SizeKB, section: sec, destructive: destructive, paths: paths,
		})
	}
	for i := range tools {
		t := tools[i]
		m.cleanupItems = append(m.cleanupItems, &litem{
			label: t.Label, sizeKB: t.SizeKB, section: secToolClean, tool: &t,
		})
	}
	rank := map[string]int{}
	for i, s := range cleanupSectionOrder {
		rank[s] = i
	}
	bySize := m.bySize
	sort.SliceStable(m.cleanupItems, func(i, j int) bool {
		a, b := m.cleanupItems[i], m.cleanupItems[j]
		if rank[a.section] != rank[b.section] {
			return rank[a.section] < rank[b.section]
		}
		if bySize {
			return a.sizeKB > b.sizeKB
		}
		return a.label < b.label
	})
}

// selectedCleanup returns the currently selected cleanup items.
func (m Model) selectedCleanup() []*litem {
	var out []*litem
	for _, it := range m.cleanupItems {
		if it.selected {
			out = append(out, it)
		}
	}
	return out
}

// keyCleanup handles Cleanup keys.
func (m Model) keyCleanup(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.scanning || m.cleaning {
		return m, nil
	}
	switch msg.String() {
	case "s":
		m.scanning = true
		m.cleanupItems = nil
		m.cleanDone = false
		return m, tea.Batch(m.spinner.Tick, m.scanCmd())
	case "up", "k":
		if m.cleanCursor > 0 {
			m.cleanCursor--
		}
	case "down", "j":
		if m.cleanCursor < len(m.cleanupItems)-1 {
			m.cleanCursor++
		}
	case " ", "x":
		if it := m.cleanupCurrent(); it != nil {
			it.selected = !it.selected
		}
	case "a":
		all := m.allCleanupSelected()
		for _, it := range m.cleanupItems {
			it.selected = !all
		}
	case "enter":
		if len(m.selectedCleanup()) == 0 {
			return m, nil
		}
		m.cleaning = true
		return m, tea.Batch(m.spinner.Tick, m.cleanCmd())
	}
	return m, nil
}

func (m Model) cleanupCurrent() *litem {
	if m.cleanCursor < 0 || m.cleanCursor >= len(m.cleanupItems) {
		return nil
	}
	return m.cleanupItems[m.cleanCursor]
}

func (m Model) allCleanupSelected() bool {
	if len(m.cleanupItems) == 0 {
		return false
	}
	for _, it := range m.cleanupItems {
		if !it.selected {
			return false
		}
	}
	return true
}

// viewCleanup renders the scan state, the selectable list, or the result.
func (m Model) viewCleanup(w int) string {
	var b strings.Builder
	b.WriteString(styleTitle.Render("Cleanup") + "\n\n")

	if m.scanning {
		b.WriteString(m.spinner.View() + styleDim.Render(" Scanning for leftovers & caches…") + "\n")
		return b.String()
	}
	if m.cleaning {
		b.WriteString(m.spinner.View() + styleDim.Render(" Cleaning selection…") + "\n")
		return b.String()
	}
	if m.cleanDone {
		b.WriteString(styleGood.Render("✓ Reclaimed "+humanize.Size(m.cleanFreedKB)) + "\n\n")
		b.WriteString(styleDim.Render("Press s to scan again.") + "\n")
		return b.String()
	}
	if len(m.cleanupItems) == 0 {
		b.WriteString(styleDim.Render("Press ") + styleBody.Render("s") +
			styleDim.Render(" to scan for reclaimable space.") + "\n")
		return b.String()
	}

	var total, selTotal int64
	lastSec := ""
	for i, it := range m.cleanupItems {
		total += it.sizeKB
		if it.selected {
			selTotal += it.sizeKB
		}
		if it.section != lastSec {
			style := styleHeading
			if it.section == secToolchain {
				style = styleDanger
			}
			b.WriteString("\n" + style.Render("── "+it.section+" ──") + "\n")
			lastSec = it.section
		}
		check := "[ ]"
		if it.selected {
			check = styleGood.Render("[x]")
		}
		label := padRight(truncate(it.label, 32), 32)
		size := styleGood.Render(humanize.Size(it.sizeKB))
		cursor := "  "
		if i == m.cleanCursor {
			cursor = styleCursor.Render("▸ ")
		}
		b.WriteString(cursor + check + " " + styleBody.Render(label) + " " + size + "\n")
	}
	b.WriteString("\n" + styleDim.Render("Selected ") + styleGood.Render(humanize.Size(selTotal)) +
		styleDim.Render(" of ") + styleBody.Render(humanize.Size(total)) + "\n")
	return b.String()
}

// truncate shortens s to n runes with an ellipsis when needed.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}
