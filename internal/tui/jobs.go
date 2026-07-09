// jobs.go renders the Jobs module: a live list of persisted maintenance jobs
// with an inline creation form. Users add expire/watch jobs, toggle them,
// delete them, and trigger a "run due" sweep — all in-place. The form is a real
// multi-field editor (text inputs + toggle rows), not a sequential prompt.
package tui

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/BadRat-in/fs-janitor/internal/duration"
	"github.com/BadRat-in/fs-janitor/internal/job"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// Form field indices (fixed layout; some are inactive for expire jobs).
const (
	fKind = iota
	fPath
	fDur
	fBasis
	fAction
	fPat
	fRec
	fDry
	fSubmit
	fCount
)

// jobForm is the inline new-job editor.
type jobForm struct {
	kind      job.Kind
	action    job.Action
	basis     job.Basis
	recursive bool
	dryRun    bool
	path      textinput.Model
	dur       textinput.Model
	pat       textinput.Model
	focus     int
	err       string
}

// newJobForm builds a fresh form defaulting to a watch job → Trash.
func newJobForm() *jobForm {
	path := textinput.New()
	path.Placeholder = "~/Downloads"
	path.Prompt = ""
	path.Focus()
	dur := textinput.New()
	dur.Placeholder = "30d"
	dur.Prompt = ""
	pat := textinput.New()
	pat.Placeholder = "*.zip *.dmg   (optional)"
	pat.Prompt = ""
	return &jobForm{
		kind: job.KindWatch, action: job.ActionTrash, basis: job.BasisModified,
		path: path, dur: dur, pat: pat, focus: fPath,
	}
}

// active reports whether a field participates for the current job kind.
func (f *jobForm) active(i int) bool {
	if f.kind == job.KindExpire {
		switch i {
		case fBasis, fPat, fRec:
			return false
		}
	}
	return true
}

// focusText syncs textinput focus with the current field.
func (f *jobForm) focusText() {
	f.path.Blur()
	f.dur.Blur()
	f.pat.Blur()
	switch f.focus {
	case fPath:
		f.path.Focus()
	case fDur:
		f.dur.Focus()
	case fPat:
		f.pat.Focus()
	}
}

// move advances focus by delta, skipping inactive fields and wrapping.
func (f *jobForm) move(delta int) {
	for {
		f.focus = (f.focus + delta + fCount) % fCount
		if f.active(f.focus) {
			break
		}
	}
	f.focusText()
}

// keyJobs handles the Jobs list keys (form input is handled by updateForm).
func (m Model) keyJobs(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "n":
		m.form = newJobForm()
		return m, textinput.Blink
	case "up", "k":
		if m.jobCursor > 0 {
			m.jobCursor--
		}
	case "down", "j":
		if m.jobCursor < len(m.jobs)-1 {
			m.jobCursor++
		}
	case "e":
		if j := m.currentJob(); j != nil {
			_ = m.app.SetJobEnabled(j.ID, !j.Enabled)
			return m, m.loadJobsCmd()
		}
	case "x":
		if j := m.currentJob(); j != nil {
			_ = m.app.RemoveJob(j.ID)
			return m, m.loadJobsCmd()
		}
	case "r":
		return m, tea.Batch(m.spinner.Tick, m.runDueCmd())
	}
	return m, nil
}

func (m Model) currentJob() *job.Job {
	if m.jobCursor < 0 || m.jobCursor >= len(m.jobs) {
		return nil
	}
	return &m.jobs[m.jobCursor]
}

// runDueCmd runs every due job and reports how many acted and how much freed.
func (m Model) runDueCmd() tea.Cmd {
	a, now := m.app, m.now
	return func() tea.Msg {
		results, _ := a.RunDue(false, now())
		var freed int64
		acted := 0
		for _, r := range results {
			freed += r.FreedKB
			if len(r.Matched) > 0 {
				acted++
			}
		}
		return runDoneMsg{freedKB: freed, count: acted}
	}
}

// updateForm handles all key input while the new-job form is open.
func (m Model) updateForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := m.form
	switch msg.String() {
	case "esc":
		m.form = nil
		return m, nil
	case "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	case "up", "shift+tab":
		f.move(-1)
		return m, nil
	case "down", "tab":
		f.move(1)
		return m, nil
	case "enter":
		if f.focus == fSubmit {
			return m.submitForm()
		}
		f.move(1)
		return m, nil
	case " ", "left", "right":
		if m.toggleField(msg.String()) {
			return m, nil
		}
	}
	// Otherwise, feed the key to the focused text input.
	var cmd tea.Cmd
	switch f.focus {
	case fPath:
		f.path, cmd = f.path.Update(msg)
	case fDur:
		f.dur, cmd = f.dur.Update(msg)
	case fPat:
		f.pat, cmd = f.pat.Update(msg)
	}
	return m, cmd
}

// toggleField flips the toggle-style field under focus. It returns false for
// text fields (where space/arrows should edit text instead). For text fields a
// space must reach the input, so only left/right are intercepted there — but
// those fields return false so the caller forwards the key.
func (m Model) toggleField(key string) bool {
	f := m.form
	switch f.focus {
	case fKind:
		if f.kind == job.KindWatch {
			f.kind = job.KindExpire
		} else {
			f.kind = job.KindWatch
		}
		return true
	case fBasis:
		f.basis = cycleBasis(f.basis)
		return true
	case fAction:
		if f.action == job.ActionTrash {
			f.action = job.ActionDelete
		} else {
			f.action = job.ActionTrash
		}
		return true
	case fRec:
		f.recursive = !f.recursive
		return true
	case fDry:
		f.dryRun = !f.dryRun
		return true
	}
	return false
}

// cycleBasis rotates the age basis for watch jobs.
func cycleBasis(b job.Basis) job.Basis {
	switch b {
	case job.BasisModified:
		return job.BasisBirth
	case job.BasisBirth:
		return job.BasisAccessed
	default:
		return job.BasisModified
	}
}

// submitForm validates the form, persists the job, and closes the form on
// success (leaving an error message on failure).
func (m Model) submitForm() (tea.Model, tea.Cmd) {
	f := m.form
	path := expandHome(strings.TrimSpace(f.path.Value()), m.app.Home)
	if path == "" {
		f.err = "path is required"
		return m, nil
	}
	dur, err := duration.Parse(strings.TrimSpace(f.dur.Value()))
	if err != nil {
		f.err = "duration: " + err.Error()
		return m, nil
	}
	var j job.Job
	if f.kind == job.KindExpire {
		j = job.NewExpire(path, dur, f.action, m.now())
	} else {
		j = job.NewWatch(path, dur, f.basis, f.action, m.now())
		j.Patterns = splitFields(f.pat.Value())
		j.Recursive = f.recursive
	}
	j.DryRun = f.dryRun
	if _, err := m.app.AddJob(j); err != nil {
		f.err = err.Error()
		return m, nil
	}
	m.form = nil
	m.status = "Job created."
	return m, m.loadJobsCmd()
}

// viewJobs renders the job list plus a detail panel for the highlighted job.
func (m Model) viewJobs(w int) string {
	var b strings.Builder
	b.WriteString(panelTitle("Jobs") + "\n\n")
	if len(m.jobs) == 0 {
		b.WriteString(styleDim.Render("No jobs yet. Press ") + styleGood.Render("n") +
			styleDim.Render(" to create one.") + "\n\n")
		b.WriteString(styleFaint.Render("Example: watch ~/Downloads, trash files older than 30d.") + "\n")
		return b.String()
	}
	for i, j := range m.jobs {
		state := "●"
		stateStyle := styleGood
		if !j.Enabled {
			state, stateStyle = "○", styleFaint
		}
		kind := padRight(string(j.Kind), 7)
		desc := truncate(j.Describe(), maxInt(20, w-14))
		if i == m.jobCursor {
			b.WriteString(styleCursor.Render("▌") +
				rowSelected.Render(" "+state+" "+kind+desc) + "\n")
			continue
		}
		b.WriteString("  " + stateStyle.Render(state) + " " +
			styleHeading.Render(kind) + styleBody.Render(desc) + "\n")
	}

	// Detail panel for the selected job.
	if j := m.currentJob(); j != nil {
		b.WriteString("\n" + styleFaint.Render(strings.Repeat("─", minInt(w, 48))) + "\n")
		b.WriteString(detailRow("Target", j.Path))
		b.WriteString(detailRow("Action", string(j.Action)))
		if j.Kind == job.KindWatch {
			b.WriteString(detailRow("Age basis", string(j.Basis)))
			if len(j.Patterns) > 0 {
				b.WriteString(detailRow("Patterns", strings.Join(j.Patterns, " ")))
			}
			b.WriteString(detailRow("Recursive", yesNo(j.Recursive)))
		} else {
			b.WriteString(detailRow("Due", j.DueAt.Format("2006-01-02 15:04")))
		}
		if j.DryRun {
			b.WriteString(detailRow("Mode", "dry-run"))
		}
		last := "never"
		if !j.LastRun.IsZero() {
			last = j.LastRun.Format("2006-01-02 15:04")
		}
		b.WriteString(detailRow("Last run", last))
	}
	return b.String()
}

// detailRow renders one aligned "label: value" line in the job detail panel.
func detailRow(label, value string) string {
	return styleDim.Render("  "+padRight(label, 11)) + styleBody.Render(value) + "\n"
}

// yesNo renders a boolean as yes/no.
func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

// viewForm renders the inline new-job editor.
func (m Model) viewForm() string {
	f := m.form
	var b strings.Builder
	b.WriteString(styleTitle.Render("New Job") + "\n\n")

	row := func(idx int, label, val string) {
		cursor := "  "
		if f.focus == idx {
			cursor = styleCursor.Render("▸ ")
		}
		if !f.active(idx) {
			b.WriteString("  " + styleDim.Render(padRight(label, 12)+val) + "\n")
			return
		}
		b.WriteString(cursor + styleBody.Render(padRight(label, 12)) + val + "\n")
	}
	toggle := func(on bool, onText, offText string) string {
		if on {
			return styleGood.Render(onText)
		}
		return styleDim.Render(offText)
	}

	row(fKind, "Type", styleHeading.Render(string(f.kind)))
	row(fPath, "Path", f.path.View())
	row(fDur, "Duration", f.dur.View())
	row(fBasis, "Age basis", styleBody.Render(string(f.basis)))
	actStyle := styleGood
	if f.action == job.ActionDelete {
		actStyle = styleDanger
	}
	row(fAction, "Action", actStyle.Render(string(f.action)))
	row(fPat, "Patterns", f.pat.View())
	row(fRec, "Recursive", toggle(f.recursive, "yes", "no"))
	row(fDry, "Dry-run", toggle(f.dryRun, "yes", "no"))

	submit := styleDim.Render("[ submit ]")
	if f.focus == fSubmit {
		submit = styleNavActive.Render(" submit ")
	}
	b.WriteString("\n  " + submit + "\n")
	if f.err != "" {
		b.WriteString("\n" + styleDanger.Render("✗ "+f.err) + "\n")
	}
	b.WriteString("\n" + styleDim.Render("↑/↓ field · space/←→ toggle · enter next/submit · esc cancel") + "\n")
	return b.String()
}

// ---- small utilities ----

// expandHome replaces a leading ~ with the home directory and makes the path
// absolute where possible.
func expandHome(p, home string) string {
	if p == "" {
		return ""
	}
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	_ = os.PathSeparator
	return p
}

// splitFields splits a whitespace/comma-separated glob list into trimmed globs.
func splitFields(s string) []string {
	repl := strings.NewReplacer(",", " ")
	fields := strings.Fields(repl.Replace(s))
	if len(fields) == 0 {
		return nil
	}
	return fields
}
