// Package tui implements FS Janitor's real, full-screen terminal UI: a
// persistent dashboard shell (not a linear question-and-answer wizard).
//
// Every screen shares the same chrome — a header carrying the live maintenance
// score, a left module rail (Dashboard · Cleanup · Jobs · History · Settings),
// a scrollable content pane, and a context-sensitive footer of key hints. The
// active module is switched with number keys or Tab and its content is rendered
// into the pane, so the user is always oriented inside one application rather
// than marched through a script.
//
// The model owns all screen state; per-module behaviour lives in sibling files
// (dashboard.go, cleanup.go, jobs.go, history.go, settings.go). Every effectful
// operation (scanning, cleaning, running jobs, computing the score) is performed
// through the injected *app.App via tea.Cmd goroutines, keeping Update pure and
// the UI responsive.
package tui

import (
	"os"
	"strings"
	"time"

	"github.com/BadRat-in/fs-janitor/internal/analytics"
	"github.com/BadRat-in/fs-janitor/internal/app"
	"github.com/BadRat-in/fs-janitor/internal/job"
	"github.com/BadRat-in/fs-janitor/internal/launchd"
	"github.com/BadRat-in/fs-janitor/internal/scan"
	"github.com/BadRat-in/fs-janitor/internal/store"
	"github.com/BadRat-in/fs-janitor/internal/toolclean"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// module identifies the active screen.
type module int

const (
	modDashboard module = iota
	modCleanup
	modJobs
	modHistory
	modSettings
	moduleCount
)

// navLabels are the rail entries in module order.
var navLabels = []string{"Dashboard", "Cleanup", "Jobs", "History", "Settings"}

// Model is the root Bubble Tea model for the whole application.
type Model struct {
	app *app.App
	now func() time.Time

	width, height int
	active        module
	spinner       spinner.Model
	quitting      bool
	status        string // transient one-line status shown in the footer
	showHelp      bool   // '?' overlay
	confirm       string // non-empty → a y/n confirmation modal is shown

	// Dashboard
	report       *analytics.Report
	loadingScore bool

	// Cleanup
	scanning     bool
	cleaning     bool
	cleanupItems []*litem
	cleanCursor  int
	cleanFreedKB int64
	cleanDone    bool
	cleanDrill   bool // show the highlighted group's paths

	// Jobs
	jobs      []job.Job
	jobCursor int
	form      *jobForm // non-nil while adding a job

	// History
	runs       []store.Run
	histOffset int

	// Settings
	setCursor         int
	includeToolchains bool
	bySize            bool
	autoInstalled     bool
}

// litem is one selectable cleanup line (a leftover/cache group, or a tool-native
// cleanup). Exactly one of paths/tool is set.
type litem struct {
	label       string
	sizeKB      int64
	section     string
	destructive bool
	selected    bool
	paths       []string
	tool        *toolclean.Cleanup
}

// New builds the root model over a wired app service. now is injected so the
// scheduler/tests control the clock; pass time.Now in production.
func New(a *app.App, now func() time.Time) Model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(colAccent)
	return Model{
		app:           a,
		now:           now,
		active:        modDashboard,
		spinner:       sp,
		width:         90,
		height:        28,
		loadingScore:  true,
		autoInstalled: launchd.IsInstalled(a.Home, os.Stat),
	}
}

// Run starts the program on the alt screen with mouse tracking enabled, and
// blocks until the user quits. Mouse support powers wheel scrolling and
// click-to-navigate (see handleMouse).
func Run(a *app.App, now func() time.Time) error {
	_, err := tea.NewProgram(New(a, now), tea.WithAltScreen(), tea.WithMouseCellMotion()).Run()
	return err
}

// ---- async messages ----

type scoreMsg struct{ report analytics.Report }
type scanMsg struct {
	res   scan.Result
	tools []toolclean.Cleanup
}
type cleanDoneMsg struct{ freedKB int64 }
type jobsMsg struct{ jobs []job.Job }
type historyMsg struct{ runs []store.Run }
type runDoneMsg struct {
	freedKB int64
	count   int
}
type statusMsg string

// Init kicks off the initial dashboard score load.
func (m Model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.loadScoreCmd(), m.loadJobsCmd(), m.loadHistoryCmd())
}

// Update is the central dispatch: global keys and window sizing first, then
// per-module handling.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case scoreMsg:
		r := msg.report
		m.report = &r
		m.loadingScore = false
		return m, nil

	case scanMsg:
		m.scanning = false
		m.buildCleanupItems(msg.res, msg.tools)
		return m, nil

	case cleanDoneMsg:
		m.cleaning = false
		m.cleanDone = true
		m.cleanFreedKB = msg.freedKB
		return m, tea.Batch(m.loadScoreCmd(), m.loadHistoryCmd())

	case jobsMsg:
		m.jobs = msg.jobs
		if m.jobCursor >= len(m.jobs) {
			m.jobCursor = maxInt(0, len(m.jobs)-1)
		}
		return m, nil

	case historyMsg:
		m.runs = msg.runs
		return m, nil

	case runDoneMsg:
		m.status = statusRun(msg.count, msg.freedKB)
		return m, tea.Batch(m.loadJobsCmd(), m.loadHistoryCmd(), m.loadScoreCmd())

	case statusMsg:
		m.status = string(msg)
		return m, nil

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

// handleMouse routes mouse input: the wheel scrolls the active module's list,
// and a left click on the module rail switches modules. Clicks are ignored
// while the job form modal is open.
func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.form != nil {
		return m, nil
	}
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		m.scroll(-1)
		return m, nil
	case tea.MouseButtonWheelDown:
		m.scroll(1)
		return m, nil
	}
	if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
		if mod, ok := m.navHit(msg.X, msg.Y); ok {
			m.active = mod
			m.status = ""
			return m, m.onEnterModule()
		}
	}
	return m, nil
}

// scroll moves the active module's cursor/offset by delta (used by the wheel and
// mirrors the arrow keys), clamped to valid bounds.
func (m *Model) scroll(delta int) {
	switch m.active {
	case modCleanup:
		m.cleanCursor = clamp(m.cleanCursor+delta, 0, len(m.cleanupItems)-1)
	case modJobs:
		m.jobCursor = clamp(m.jobCursor+delta, 0, len(m.jobs)-1)
	case modHistory:
		m.histOffset = clamp(m.histOffset+delta, 0, maxInt(0, len(m.runs)-1))
	case modSettings:
		m.setCursor = clamp(m.setCursor+delta, 0, len(settingLabels)-1)
	}
}

// navHit maps a click coordinate to a module rail entry. It returns ok=false
// when the click lands outside the rail. Geometry mirrors View: the rail begins
// one row below the header, indented by the rail's top padding.
func (m Model) navHit(x, y int) (module, bool) {
	headerH := lipgloss.Height(m.viewHeader())
	railW := lipgloss.Width(styleRail.Render(m.viewNav()))
	idx := y - headerH - 1 // -1 for the rail's top padding row
	if x <= railW && idx >= 0 && idx < len(navLabels) {
		return module(idx), true
	}
	return 0, false
}

// clamp constrains v to [lo, hi]; if hi < lo it returns lo.
func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// handleKey routes a key press. When a modal (the job form) is open it captures
// input; otherwise global keys (quit, module switch) are handled before the
// active module's own handler.
func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.form != nil {
		return m.updateForm(msg)
	}
	// Confirmation modal captures input until answered.
	if m.confirm != "" {
		switch msg.String() {
		case "y", "Y", "enter":
			return m.runConfirmed()
		case "n", "N", "esc", "q":
			m.confirm = ""
		}
		return m, nil
	}
	// Help overlay: any key (except quit) dismisses it.
	if m.showHelp {
		if msg.String() == "ctrl+c" {
			m.quitting = true
			return m, tea.Quit
		}
		m.showHelp = false
		return m, nil
	}
	switch msg.String() {
	case "ctrl+c", "q":
		m.quitting = true
		return m, tea.Quit
	case "?":
		m.showHelp = true
		return m, nil
	case "tab":
		m.active = (m.active + 1) % moduleCount
		return m, m.onEnterModule()
	case "shift+tab":
		m.active = (m.active + moduleCount - 1) % moduleCount
		return m, m.onEnterModule()
	case "1", "2", "3", "4", "5":
		m.active = module(msg.String()[0] - '1')
		return m, m.onEnterModule()
	}
	m.status = ""
	switch m.active {
	case modDashboard:
		return m.keyDashboard(msg)
	case modCleanup:
		return m.keyCleanup(msg)
	case modJobs:
		return m.keyJobs(msg)
	case modHistory:
		return m.keyHistory(msg)
	case modSettings:
		return m.keySettings(msg)
	}
	return m, nil
}

// onEnterModule refreshes data a module needs when it becomes active.
func (m Model) onEnterModule() tea.Cmd {
	switch m.active {
	case modJobs:
		return m.loadJobsCmd()
	case modHistory:
		return m.loadHistoryCmd()
	case modDashboard:
		if m.report == nil && !m.loadingScore {
			m.loadingScore = true
			return m.loadScoreCmd()
		}
	}
	return nil
}

// runConfirmed executes the pending confirmed action. Currently the only
// confirmation guards a permanent Cleanup deletion.
func (m Model) runConfirmed() (tea.Model, tea.Cmd) {
	m.confirm = ""
	m.cleaning = true
	return m, tea.Batch(m.spinner.Tick, m.cleanCmd())
}

// View assembles the persistent chrome — header, module rail, bordered content
// panel, footer — around the active module's content, with modal overlays for
// help and confirmations.
func (m Model) View() string {
	if m.quitting {
		return ""
	}
	header := m.viewHeader()
	footer := m.viewFooter()

	bodyH := m.height - lipgloss.Height(header) - lipgloss.Height(footer)
	if bodyH < 6 {
		bodyH = 6
	}
	rail := styleRail.Height(bodyH).Render(m.viewNav())

	// The content panel is a rounded box filling the remaining width/height.
	innerW := m.width - lipgloss.Width(rail) - 4 // borders + padding
	if innerW < 24 {
		innerW = 24
	}
	panelH := bodyH - 2 // account for the panel's top/bottom border
	if panelH < 4 {
		panelH = 4
	}
	body := m.panelBody(innerW, panelH)
	panel := stylePanel.Width(innerW).Height(panelH).Render(body)
	row := lipgloss.JoinHorizontal(lipgloss.Top, rail, panel)
	return lipgloss.JoinVertical(lipgloss.Left, header, row, footer)
}

// panelBody returns the content for the panel: a modal (help/confirm/form) when
// one is active, otherwise the active module's own view, always led by a title.
func (m Model) panelBody(w, h int) string {
	switch {
	case m.showHelp:
		return m.viewHelp()
	case m.confirm != "":
		return m.viewConfirm(w)
	case m.form != nil:
		return m.viewForm()
	}
	return m.viewContent(w)
}

// viewHeader renders the title bar with the live maintenance-score badge.
func (m Model) viewHeader() string {
	title := styleTitle.Render("FS Janitor") +
		styleFaint.Render("  ·  filesystem maintenance for macOS")
	right := styleDim.Render("score computing…")
	if m.report != nil {
		col := scoreColor(m.report.Score)
		right = styleDim.Render("Maintenance ") +
			badge(itoa(m.report.Score)+"/100 "+m.report.Grade, col)
	}
	gap := m.width - 2 - lipgloss.Width(title) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	line := title + strings.Repeat(" ", gap) + right
	return styleHeader.Width(m.width - 2).Render(line)
}

// viewNav renders the left module rail: icon + label per module, with the active
// entry filled and flagged by a pink leading bar.
func (m Model) viewNav() string {
	var b strings.Builder
	for i, label := range navLabels {
		text := " " + navIcons[i] + "  " + label + " "
		if module(i) == m.active {
			b.WriteString(styleCursor.Render("▌") + styleNavActive.Render(text))
		} else {
			b.WriteString(" " + styleNav.Render(text))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// viewContent dispatches to the active module's renderer.
func (m Model) viewContent(w int) string {
	switch m.active {
	case modDashboard:
		return m.viewDashboard(w)
	case modCleanup:
		return m.viewCleanup(w)
	case modJobs:
		return m.viewJobs(w)
	case modHistory:
		return m.viewHistory(w)
	case modSettings:
		return m.viewSettings(w)
	}
	return ""
}

// viewConfirm renders a centred y/n confirmation modal.
func (m Model) viewConfirm(w int) string {
	var b strings.Builder
	b.WriteString(styleDanger.Render("⚠  Confirm") + "\n\n")
	b.WriteString(styleBody.Render(m.confirm) + "\n\n")
	b.WriteString(keyLegend([2]string{"y", "yes"}, [2]string{"n", "cancel"}))
	return b.String()
}

// viewHelp renders the full keybinding reference overlay.
func (m Model) viewHelp() string {
	var b strings.Builder
	b.WriteString(panelTitle("Keyboard & Mouse") + "\n\n")
	sec := func(title string, rows ...[2]string) {
		b.WriteString(styleHeading.Render(title) + "\n")
		for _, r := range rows {
			b.WriteString("  " + keyHint(padRight(r[0], 12), r[1]) + "\n")
		}
		b.WriteString("\n")
	}
	sec("Global",
		[2]string{"1–5 / tab", "switch module"},
		[2]string{"wheel", "scroll list"},
		[2]string{"click", "select a module in the rail"},
		[2]string{"?", "toggle this help"},
		[2]string{"q", "quit"})
	sec("Cleanup",
		[2]string{"s", "scan"}, [2]string{"space", "select"},
		[2]string{"a", "select all"}, [2]string{"d", "show paths"},
		[2]string{"enter", "clean selection"})
	sec("Jobs",
		[2]string{"n", "new job"}, [2]string{"e", "enable/disable"},
		[2]string{"x", "delete"}, [2]string{"r", "run due now"})
	b.WriteString(styleFaint.Render("press any key to close"))
	return b.String()
}

// viewFooter renders context key hints plus any transient status message.
func (m Model) viewFooter() string {
	hints := m.footerHints()
	if m.status != "" {
		hints = styleGood.Render("• "+m.status) + styleFaint.Render("    ") + hints
	}
	return styleFooter.Width(m.width - 2).Render(hints)
}

// footerHints returns the key legend for the active module (plus the global set).
func (m Model) footerHints() string {
	var local [][2]string
	switch m.active {
	case modDashboard:
		local = [][2]string{{"r", "rescan"}}
	case modCleanup:
		local = [][2]string{{"s", "scan"}, {"space", "select"}, {"a", "all"}, {"d", "paths"}, {"enter", "clean"}}
	case modJobs:
		local = [][2]string{{"n", "new"}, {"e", "toggle"}, {"x", "delete"}, {"r", "run due"}}
	case modHistory:
		local = [][2]string{{"↑↓", "scroll"}}
	case modSettings:
		local = [][2]string{{"space", "toggle"}}
	}
	global := [][2]string{{"tab", "switch"}, {"?", "help"}, {"q", "quit"}}
	return keyLegend(append(local, global...)...)
}

// ---- shared commands ----

func (m Model) loadScoreCmd() tea.Cmd {
	a, now := m.app, m.now
	return func() tea.Msg { return scoreMsg{report: a.Score(now())} }
}

func (m Model) loadJobsCmd() tea.Cmd {
	a := m.app
	return func() tea.Msg {
		js, _ := a.Jobs()
		return jobsMsg{jobs: js}
	}
}

func (m Model) loadHistoryCmd() tea.Cmd {
	a := m.app
	return func() tea.Msg {
		rs, _ := a.Store.History(50)
		return historyMsg{runs: rs}
	}
}

// ---- tiny helpers ----

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
