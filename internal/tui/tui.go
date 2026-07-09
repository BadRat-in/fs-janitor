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

// Run starts the program on the alt screen and blocks until the user quits.
func Run(a *app.App, now func() time.Time) error {
	_, err := tea.NewProgram(New(a, now), tea.WithAltScreen()).Run()
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

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

// handleKey routes a key press. When a modal (the job form) is open it captures
// input; otherwise global keys (quit, module switch) are handled before the
// active module's own handler.
func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.form != nil {
		return m.updateForm(msg)
	}
	switch msg.String() {
	case "ctrl+c", "q":
		m.quitting = true
		return m, tea.Quit
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

// View assembles the persistent chrome around the active module's content.
func (m Model) View() string {
	if m.quitting {
		return ""
	}
	header := m.viewHeader()
	footer := m.viewFooter()

	railW := 14
	bodyH := m.height - lipgloss.Height(header) - lipgloss.Height(footer) - 1
	if bodyH < 4 {
		bodyH = 4
	}
	rail := styleRail.Height(bodyH).Render(m.viewNav())
	contentW := m.width - lipgloss.Width(rail) - 2
	if contentW < 20 {
		contentW = 20
	}
	content := lipgloss.NewStyle().Width(contentW).Height(bodyH).Padding(0, 1).Render(m.viewContent(contentW))
	body := lipgloss.JoinHorizontal(lipgloss.Top, rail, content)
	_ = railW
	return lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
}

// viewHeader renders the title bar with the live maintenance score.
func (m Model) viewHeader() string {
	title := styleTitle.Render("🧹 FS Janitor")
	score := styleDim.Render("  score: computing…")
	if m.report != nil {
		g := m.report.Grade
		st := styleGood
		if m.report.Score < 75 {
			st = styleWarn
		}
		if m.report.Score < 40 {
			st = styleDanger
		}
		score = "  " + styleDim.Render("Maintenance ") + st.Render(itoa(m.report.Score)+"/100 ("+g+")")
	}
	line := lipgloss.JoinHorizontal(lipgloss.Left, title, score)
	return styleHeader.Width(m.width - 2).Render(line)
}

// viewNav renders the left module rail with the active entry highlighted.
func (m Model) viewNav() string {
	out := ""
	for i, label := range navLabels {
		row := styleNav.Render(itoa(i+1) + " " + label)
		if module(i) == m.active {
			row = styleNavActive.Render(itoa(i+1) + " " + label)
		}
		out += row + "\n"
	}
	return out
}

// viewContent dispatches to the active module's renderer.
func (m Model) viewContent(w int) string {
	if m.form != nil {
		return m.viewForm()
	}
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

// viewFooter renders context key hints plus any transient status message.
func (m Model) viewFooter() string {
	hints := m.footerHints()
	if m.status != "" {
		hints = styleGood.Render(m.status) + styleDim.Render("   ·   ") + hints
	}
	return styleFooter.Width(m.width - 2).Render(hints)
}

// footerHints returns the key legend for the active module.
func (m Model) footerHints() string {
	global := "tab switch · 1-5 modules · q quit"
	var local string
	switch m.active {
	case modDashboard:
		local = "r rescan"
	case modCleanup:
		local = "s scan · ↑↓ move · space select · a all · enter clean"
	case modJobs:
		local = "↑↓ move · n new · e enable · x delete · r run due"
	case modHistory:
		local = "↑↓ scroll"
	case modSettings:
		local = "↑↓ move · space toggle · enter apply"
	}
	if local != "" {
		return styleDim.Render(local + "  ·  " + global)
	}
	return styleDim.Render(global)
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
