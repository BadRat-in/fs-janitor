// Package tui implements CleanX's Bubble Tea terminal UI, replacing the raw
// ANSI escape sequences of the reference script with a proper full-screen,
// terminal-agnostic interface. It drives the whole interactive flow:
//
//	settings → scan (spinner) → multi-select (checkboxes, sizes, drill-down)
//	         → confirm (destructive) → run (progress bar) → summary (+ Time
//	         Machine snapshots)
//
// All side effects — scanning, deleting, running tool cleanups, Time Machine —
// are injected via Actions, so the model's state transitions are decoupled from
// the OS and the pure selection/toggle logic is unit-tested.
package cleanui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/BadRat-in/fs-janitor/internal/cleaner"
	"github.com/BadRat-in/fs-janitor/internal/humanize"
	"github.com/BadRat-in/fs-janitor/internal/scan"
	"github.com/BadRat-in/fs-janitor/internal/toolclean"
	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Actions injects the effectful operations the UI performs. Wiring lives in
// cmd; tests supply fakes.
type Actions struct {
	Scan         func(scan.Options) scan.Result
	ToolCleanups func() []toolclean.Cleanup
	DeletePaths  func(paths []string) cleaner.Result
	RunTool      func(c toolclean.Cleanup) (freedKB int64, err error)
	ListSnaps    func() []string
	DeleteSnap   func(id string) error
}

type screen int

const (
	screenSettings screen = iota
	screenScanning
	screenSelect
	screenConfirm
	screenRunning
	screenSummary
)

// item is one selectable line: a leftover/dev group (delete its paths) or a
// tool cleanup (run its command). Exactly one of paths/tool is set.
type item struct {
	label       string
	sizeKB      int64
	section     string
	destructive bool
	selected    bool
	paths       []string           // set for delete-style items
	tool        *toolclean.Cleanup // set for run-style items
}

// Section labels and ordering.
const (
	secDevCache  = "Developer / Build-Tool Caches (re-downloadable)"
	secToolchain = "Toolchains / Runtimes — deleting UNINSTALLS them"
	secLibrary   = "App Library Leftovers"
	secToolClean = "Tool-Native Cache Cleanup"
)

var sectionOrder = []string{secDevCache, secToolchain, secLibrary, secToolClean}

// Model is the Bubble Tea model.
type Model struct {
	actions Actions
	opts    scan.Options
	dryRun  bool
	bySize  bool

	screen   screen
	spinner  spinner.Model
	progress progress.Model

	settingsCursor int
	items          []*item
	rows           []int // indices into items for navigable (non-header) rows
	cursor         int   // index into rows
	drill          bool  // show highlighted item's paths

	stepIdx    int
	freedKB    int64
	failed     int
	snaps      []string
	snapPrompt bool

	quitting bool
	width    int
}

// New builds the initial model. When skipSettings is true the model starts
// scanning immediately (used by the --run/--dry-run flags), otherwise it opens
// on the settings screen.
func New(actions Actions, opts scan.Options, dryRun, bySize, skipSettings bool) Model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	start := screenSettings
	if skipSettings {
		start = screenScanning
	}
	return Model{
		actions:  actions,
		opts:     opts,
		dryRun:   dryRun,
		bySize:   bySize,
		screen:   start,
		spinner:  sp,
		progress: progress.New(progress.WithDefaultGradient()),
		width:    80,
	}
}

// ---- Messages ----

type scanDoneMsg struct {
	res   scan.Result
	tools []toolclean.Cleanup
}

// stepDoneMsg reports the result of executing one selected item. Carrying the
// freed/failed deltas in the message (rather than mutating the model from the
// command goroutine) is the correct Bubble Tea pattern — Update applies them.
type stepDoneMsg struct {
	freed  int64
	failed int
}

// Init implements tea.Model. When the model was constructed to skip settings it
// kicks off the scan (and spinner) immediately.
func (m Model) Init() tea.Cmd {
	if m.screen == screenScanning {
		return tea.Batch(m.spinner.Tick, m.scanCmd())
	}
	return nil
}

// ---- Commands ----

func (m Model) scanCmd() tea.Cmd {
	return func() tea.Msg {
		res := m.actions.Scan(m.opts)
		var tools []toolclean.Cleanup
		if m.actions.ToolCleanups != nil {
			tools = m.actions.ToolCleanups()
		}
		return scanDoneMsg{res: res, tools: tools}
	}
}

// stepCmd executes one selected item (delete or run) and reports the freed and
// failed deltas via a message. It captures only the values it needs so the
// command goroutine never touches the model.
func (m Model) stepCmd(it *item) tea.Cmd {
	dryRun := m.dryRun
	actions := m.actions
	return func() tea.Msg {
		if dryRun {
			return stepDoneMsg{}
		}
		if it.tool != nil {
			freed, err := actions.RunTool(*it.tool)
			if err != nil {
				return stepDoneMsg{failed: 1}
			}
			return stepDoneMsg{freed: freed}
		}
		r := actions.DeletePaths(it.paths)
		failed := 0
		for _, pr := range r.Paths {
			if !pr.Deleted {
				failed++
			}
		}
		return stepDoneMsg{freed: r.FreedKB, failed: failed}
	}
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.progress.Width = msg.Width - 4
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case scanDoneMsg:
		m.buildItems(msg.res, msg.tools)
		m.screen = screenSelect
		return m, nil

	case stepDoneMsg:
		m.freedKB += msg.freed
		m.failed += msg.failed
		return m.advanceStep()

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

// handleKey dispatches key presses per screen.
func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		m.quitting = true
		return m, tea.Quit
	}
	switch m.screen {
	case screenSettings:
		return m.keySettings(msg)
	case screenSelect:
		return m.keySelect(msg)
	case screenConfirm:
		return m.keyConfirm(msg)
	case screenSummary:
		return m.keySummary(msg)
	}
	return m, nil
}

// ---- Settings screen ----

// settingRows are the toggle rows on the settings screen.
var settingLabels = []string{
	"Sort results",
	"Include installed apps",
	"Require staleness gate",
	"Skip in-use files",
	"Dry-run (preview only)",
	"Include toolchains (destructive)",
}

func (m Model) keySettings(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc":
		m.quitting = true
		return m, tea.Quit
	case "up", "k":
		if m.settingsCursor > 0 {
			m.settingsCursor--
		}
	case "down", "j":
		if m.settingsCursor < len(settingLabels)-1 {
			m.settingsCursor++
		}
	case " ", "space", "left", "right", "enter":
		if msg.String() == "enter" {
			m.screen = screenScanning
			return m, tea.Batch(m.spinner.Tick, m.scanCmd())
		}
		m.toggleSetting(m.settingsCursor)
	case "s":
		m.screen = screenScanning
		return m, tea.Batch(m.spinner.Tick, m.scanCmd())
	}
	return m, nil
}

func (m *Model) toggleSetting(i int) {
	switch i {
	case 0:
		m.bySize = !m.bySize
	case 1:
		m.opts.IncludeInstalled = !m.opts.IncludeInstalled
	case 2:
		m.opts.IncludeStale = !m.opts.IncludeStale
	case 3:
		m.opts.CheckInUse = !m.opts.CheckInUse
	case 4:
		m.dryRun = !m.dryRun
	case 5:
		m.opts.IncludeToolchains = !m.opts.IncludeToolchains
	}
}

// ---- Select screen ----

// buildItems converts scan results + tool cleanups into the selectable list,
// grouped and sorted, and computes the navigable row index.
func (m *Model) buildItems(res scan.Result, tools []toolclean.Cleanup) {
	m.items = nil
	for _, g := range res.Groups {
		sec := secLibrary
		destructive := false
		switch g.Kind {
		case scan.KindDevCache:
			sec = secDevCache
		case scan.KindToolchain:
			sec = secToolchain
			destructive = true
		}
		paths := append([]string{}, g.Paths...)
		m.items = append(m.items, &item{
			label: g.Vendor, sizeKB: g.SizeKB, section: sec,
			destructive: destructive, paths: paths,
		})
	}
	for i := range tools {
		t := tools[i]
		m.items = append(m.items, &item{
			label: t.Label, sizeKB: t.SizeKB, section: secToolClean, tool: &t,
		})
	}
	m.sortItems()
	m.recomputeRows()
	m.cursor = 0
}

// sortItems orders items by section, then by size (bySize) or name.
func (m *Model) sortItems() {
	secRank := map[string]int{}
	for i, s := range sectionOrder {
		secRank[s] = i
	}
	sort.SliceStable(m.items, func(i, j int) bool {
		a, b := m.items[i], m.items[j]
		if secRank[a.section] != secRank[b.section] {
			return secRank[a.section] < secRank[b.section]
		}
		if m.bySize {
			return a.sizeKB > b.sizeKB
		}
		return a.label < b.label
	})
}

// recomputeRows records which item indices are navigable (all of them; headers
// are rendered from section boundaries).
func (m *Model) recomputeRows() {
	m.rows = m.rows[:0]
	for i := range m.items {
		m.rows = append(m.rows, i)
	}
	if m.cursor >= len(m.rows) {
		m.cursor = len(m.rows) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

func (m Model) keySelect(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc":
		m.quitting = true
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
		m.drill = false
	case "down", "j":
		if m.cursor < len(m.rows)-1 {
			m.cursor++
		}
		m.drill = false
	case " ", "space", "x":
		if it := m.current(); it != nil {
			it.selected = !it.selected
		}
	case "a":
		all := m.allSelected()
		for _, it := range m.items {
			it.selected = !all
		}
	case "d":
		m.drill = !m.drill
	case "enter":
		if !m.anySelected() {
			m.quitting = true
			return m, tea.Quit
		}
		if m.anyDestructiveSelected() && !m.dryRun {
			m.screen = screenConfirm
			return m, nil
		}
		return m.startRun()
	}
	return m, nil
}

func (m *Model) current() *item {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return nil
	}
	return m.items[m.rows[m.cursor]]
}

func (m Model) allSelected() bool {
	if len(m.items) == 0 {
		return false
	}
	for _, it := range m.items {
		if !it.selected {
			return false
		}
	}
	return true
}

func (m Model) anySelected() bool {
	for _, it := range m.items {
		if it.selected {
			return true
		}
	}
	return false
}

func (m Model) anyDestructiveSelected() bool {
	for _, it := range m.items {
		if it.selected && it.destructive {
			return true
		}
	}
	return false
}

func (m Model) selectedItems() []*item {
	var out []*item
	for _, it := range m.items {
		if it.selected {
			out = append(out, it)
		}
	}
	return out
}

// ---- Confirm screen ----

func (m Model) keyConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		return m.startRun()
	case "n", "N", "esc", "q":
		m.screen = screenSelect
		return m, nil
	}
	return m, nil
}

// ---- Run (execution) ----

func (m Model) startRun() (tea.Model, tea.Cmd) {
	m.screen = screenRunning
	m.stepIdx = 0
	m.freedKB = 0
	m.failed = 0
	sel := m.selectedItems()
	if len(sel) == 0 {
		return m.finishRun()
	}
	return m, m.stepCmd(sel[0])
}

func (m Model) advanceStep() (tea.Model, tea.Cmd) {
	m.stepIdx++
	sel := m.selectedItems()
	if m.stepIdx >= len(sel) {
		return m.finishRun()
	}
	cmd := tea.Batch(
		m.progress.SetPercent(float64(m.stepIdx)/float64(len(sel))),
		m.stepCmd(sel[m.stepIdx]),
	)
	return m, cmd
}

func (m Model) finishRun() (tea.Model, tea.Cmd) {
	if m.actions.ListSnaps != nil {
		m.snaps = m.actions.ListSnaps()
	}
	m.snapPrompt = len(m.snaps) > 0 && !m.dryRun
	m.screen = screenSummary
	return m, nil
}

// ---- Summary screen ----

func (m Model) keySummary(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.snapPrompt {
		switch msg.String() {
		case "y", "Y":
			for _, s := range m.snaps {
				if m.actions.DeleteSnap != nil {
					_ = m.actions.DeleteSnap(s)
				}
			}
			m.snaps = nil
			m.snapPrompt = false
			return m, nil
		case "n", "N":
			m.snapPrompt = false
			return m, nil
		}
	}
	switch msg.String() {
	case "q", "enter", "esc":
		m.quitting = true
		return m, tea.Quit
	}
	return m, nil
}

// Quitting reports whether the user ended the session (used by cmd to know the
// program exited normally).
func (m Model) Quitting() bool { return m.quitting }

// FreedKB exposes the reclaimed total for post-run reporting.
func (m Model) FreedKB() int64 { return m.freedKB }

// WasDryRun reports whether the run was in preview mode (no deletions).
func (m Model) WasDryRun() bool { return m.dryRun }

// ---- Styles ----

var (
	styleTitle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	styleSection = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214"))
	styleDanger  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("196"))
	styleDim     = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	styleSize    = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	styleCursor  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
)

// View implements tea.Model.
func (m Model) View() string {
	if m.quitting {
		return ""
	}
	switch m.screen {
	case screenSettings:
		return m.viewSettings()
	case screenScanning:
		return fmt.Sprintf("\n %s Scanning for leftovers...\n", m.spinner.View())
	case screenSelect:
		return m.viewSelect()
	case screenConfirm:
		return m.viewConfirm()
	case screenRunning:
		return m.viewRunning()
	case screenSummary:
		return m.viewSummary()
	}
	return ""
}

func (m Model) viewSettings() string {
	var b strings.Builder
	b.WriteString(styleTitle.Render("CleanX — Scan Settings") + "\n\n")
	for i, label := range settingLabels {
		cursor := "  "
		if i == m.settingsCursor {
			cursor = styleCursor.Render("▸ ")
		}
		b.WriteString(fmt.Sprintf("%s%s: %s\n", cursor, label, m.settingValue(i)))
	}
	b.WriteString("\n" + styleDim.Render("↑/↓ move · space toggle · enter start · q quit") + "\n")
	return b.String()
}

func (m Model) settingValue(i int) string {
	on := styleSize.Render("ON")
	off := styleDim.Render("OFF")
	b := func(v bool) string {
		if v {
			return on
		}
		return off
	}
	switch i {
	case 0:
		if m.bySize {
			return styleSize.Render("SIZE")
		}
		return styleSize.Render("NAME")
	case 1:
		return b(m.opts.IncludeInstalled)
	case 2:
		return b(m.opts.IncludeStale)
	case 3:
		return b(m.opts.CheckInUse)
	case 4:
		return b(m.dryRun)
	case 5:
		return b(m.opts.IncludeToolchains)
	}
	return ""
}

func (m Model) viewSelect() string {
	var b strings.Builder
	title := "Select what to clean"
	if m.dryRun {
		title += styleDim.Render("  (dry-run — nothing will be deleted)")
	}
	b.WriteString(styleTitle.Render(title) + "\n\n")

	lastSec := ""
	for i, it := range m.items {
		if it.section != lastSec {
			style := styleSection
			if it.section == secToolchain {
				style = styleDanger
			}
			b.WriteString("\n" + style.Render("── "+it.section+" ──") + "\n")
			lastSec = it.section
		}
		check := "[ ]"
		if it.selected {
			check = styleSize.Render("[x]")
		}
		cursor := "  "
		line := fmt.Sprintf("%s %s %-34s %10s", cursor, check, it.label, styleSize.Render(humanize.Size(it.sizeKB)))
		if i == m.rows[m.cursor] {
			line = styleCursor.Render("▸ ") + fmt.Sprintf("%s %-34s %10s", check, it.label, styleSize.Render(humanize.Size(it.sizeKB)))
			_ = cursor
		}
		b.WriteString(line + "\n")
		if m.drill && i == m.rows[m.cursor] {
			for _, p := range m.drillPaths(it) {
				b.WriteString("      " + styleDim.Render(p) + "\n")
			}
		}
	}
	b.WriteString("\n" + styleDim.Render("↑/↓ move · space select · a all · d paths · enter clean · q quit") + "\n")
	return b.String()
}

func (m Model) drillPaths(it *item) []string {
	if it.tool != nil {
		return []string{"runs: " + it.tool.Command()}
	}
	return it.paths
}

func (m Model) viewConfirm() string {
	var b strings.Builder
	b.WriteString(styleDanger.Render("⚠  Destructive selection") + "\n\n")
	b.WriteString("You selected toolchain/runtime dirs. Deleting them UNINSTALLS the tool\nor its language versions.\n\n")
	for _, it := range m.selectedItems() {
		if it.destructive {
			b.WriteString("  " + styleDanger.Render("• "+it.label) + " " + humanize.Size(it.sizeKB) + "\n")
		}
	}
	b.WriteString("\n" + styleTitle.Render("Proceed? (y/n)") + "\n")
	return b.String()
}

func (m Model) viewRunning() string {
	sel := m.selectedItems()
	done := m.stepIdx
	if done > len(sel) {
		done = len(sel)
	}
	return fmt.Sprintf("\n Cleaning %d/%d...\n\n %s\n", done, len(sel), m.progress.View())
}

func (m Model) viewSummary() string {
	var b strings.Builder
	b.WriteString(styleTitle.Render("Cleanup Summary") + "\n\n")
	if m.dryRun {
		var total int64
		for _, it := range m.selectedItems() {
			total += it.sizeKB
		}
		b.WriteString("💾 " + styleSize.Render("Potential space to reclaim: "+humanize.Size(total)) + styleDim.Render("  (dry-run)") + "\n")
	} else {
		b.WriteString("💾 " + styleSize.Render("Space reclaimed: "+humanize.Size(m.freedKB)) + "\n")
		if m.failed > 0 {
			b.WriteString(styleDanger.Render(fmt.Sprintf("   %d item(s) failed to delete.", m.failed)) + "\n")
		}
	}
	if m.snapPrompt {
		b.WriteString("\n" + styleSection.Render("Time Machine local snapshots") + "\n")
		for _, s := range m.snaps {
			b.WriteString("  • " + styleDim.Render(s) + "\n")
		}
		b.WriteString("\n" + styleTitle.Render(fmt.Sprintf("Delete all %d local snapshot(s)? (y/n)", len(m.snaps))) + "\n")
	} else {
		b.WriteString("\n" + styleDim.Render("Press q to quit.") + "\n")
	}
	return b.String()
}
