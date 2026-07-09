package cleanui

import (
	"strings"
	"testing"

	"github.com/BadRat-in/fs-janitor/internal/cleaner"
	"github.com/BadRat-in/fs-janitor/internal/scan"
	"github.com/BadRat-in/fs-janitor/internal/toolclean"
	tea "github.com/charmbracelet/bubbletea"
)

// fakeActions records calls and returns canned data.
type fakeActions struct {
	deleted   [][]string
	toolRuns  int
	snapsGone bool
}

func (f *fakeActions) build(groups []scan.Group, tools []toolclean.Cleanup, snaps []string) Actions {
	return Actions{
		Scan:         func(scan.Options) scan.Result { return scan.Result{Groups: groups} },
		ToolCleanups: func() []toolclean.Cleanup { return tools },
		DeletePaths: func(paths []string) cleaner.Result {
			f.deleted = append(f.deleted, paths)
			return cleaner.Result{FreedKB: 100}
		},
		RunTool:    func(toolclean.Cleanup) (int64, error) { f.toolRuns++; return 50, nil },
		ListSnaps:  func() []string { return snaps },
		DeleteSnap: func(string) error { f.snapsGone = true; return nil },
	}
}

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "space":
		return tea.KeyMsg{Type: tea.KeySpace}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

// send feeds a message and returns the updated Model plus any command.
func send(m Model, msg tea.Msg) (Model, tea.Cmd) {
	next, cmd := m.Update(msg)
	return next.(Model), cmd
}

// drainSteps executes step commands until the model leaves the running screen,
// simulating the Bubble Tea runtime for the synchronous fakes.
func drainSteps(m Model, cmd tea.Cmd) Model {
	for m.screen == screenRunning && cmd != nil {
		msg := cmd()
		// step commands may be batched; unwrap a BatchMsg by running each.
		m, cmd = applyMsg(m, msg)
	}
	return m
}

func applyMsg(m Model, msg tea.Msg) (Model, tea.Cmd) {
	switch mm := msg.(type) {
	case tea.BatchMsg:
		var last tea.Cmd
		for _, c := range mm {
			if c == nil {
				continue
			}
			m, last = applyMsg(m, c())
		}
		return m, last
	default:
		return send(m, msg)
	}
}

func groups() []scan.Group {
	return []scan.Group{
		{Vendor: "Brave", SizeKB: 1000, Kind: scan.KindLibrary, Paths: []string{"/b1", "/b2"}},
		{Vendor: "Cargo Registry Cache", SizeKB: 5000, Kind: scan.KindDevCache, Paths: []string{"/c"}},
		{Vendor: "Rust Toolchains", SizeKB: 9000, Kind: scan.KindToolchain, Paths: []string{"/r"}},
	}
}

// TestScanBuildsSectionedItems verifies scanDoneMsg populates items with the
// correct sections and destructive flags, and tool cleanups are appended.
func TestScanBuildsSectionedItems(t *testing.T) {
	fa := &fakeActions{}
	tools := []toolclean.Cleanup{{Label: "uv cache", SizeKB: 200}}
	m := New(fa.build(groups(), tools, nil), scan.NewOptions(), false, false, true)

	m, _ = send(m, scanDoneMsg{res: scan.Result{Groups: groups()}, tools: tools})
	if m.screen != screenSelect {
		t.Fatalf("expected screenSelect, got %v", m.screen)
	}
	if len(m.items) != 4 {
		t.Fatalf("expected 4 items (3 groups + 1 tool), got %d", len(m.items))
	}
	var toolchain, toolCleanup *item
	for _, it := range m.items {
		if it.section == secToolchain {
			toolchain = it
		}
		if it.section == secToolClean {
			toolCleanup = it
		}
	}
	if toolchain == nil || !toolchain.destructive {
		t.Error("Rust Toolchains should be in the toolchain section and destructive")
	}
	if toolCleanup == nil || toolCleanup.tool == nil {
		t.Error("uv cache should be a tool-cleanup item")
	}
}

// TestDestructiveSelectionRequiresConfirm verifies selecting a toolchain and
// pressing enter routes through the confirm screen (non-dry-run).
func TestDestructiveSelectionRequiresConfirm(t *testing.T) {
	fa := &fakeActions{}
	m := New(fa.build(groups(), nil, nil), scan.NewOptions(), false, false, true)
	m, _ = send(m, scanDoneMsg{res: scan.Result{Groups: groups()}, tools: nil})

	// Select all, then enter.
	m, _ = send(m, key("a"))
	if !m.allSelected() {
		t.Fatal("'a' should select all items")
	}
	m, _ = send(m, key("enter"))
	if m.screen != screenConfirm {
		t.Fatalf("destructive selection should require confirm, got screen %v", m.screen)
	}

	// Decline returns to select; confirm proceeds to run then summary.
	m2, _ := send(m, key("n"))
	if m2.screen != screenSelect {
		t.Errorf("'n' should return to select, got %v", m2.screen)
	}
	m, cmd := send(m, key("y"))
	m = drainSteps(m, cmd)
	if m.screen != screenSummary {
		t.Fatalf("after confirming, run should complete to summary, got %v", m.screen)
	}
	// All three groups' paths deleted (Brave 2 paths + cargo + rust).
	if len(fa.deleted) != 3 {
		t.Errorf("expected 3 delete calls, got %d", len(fa.deleted))
	}
	if m.FreedKB() != 300 {
		t.Errorf("freed = %d, want 300 (3 delete calls * 100)", m.FreedKB())
	}
}

// TestDryRunSkipsDeletion verifies that in dry-run mode enter goes straight to
// the summary without calling DeletePaths, and reports potential size.
func TestDryRunSkipsDeletion(t *testing.T) {
	fa := &fakeActions{}
	m := New(fa.build(groups(), nil, nil), scan.NewOptions(), true /*dryRun*/, false, true)
	m, _ = send(m, scanDoneMsg{res: scan.Result{Groups: groups()}, tools: nil})

	// Select only the non-destructive Brave item.
	for i, it := range m.items {
		if it.label == "Brave" {
			m.cursor = i
		}
	}
	m, _ = send(m, key("space"))
	m, cmd := send(m, key("enter"))
	m = drainSteps(m, cmd)
	if m.screen != screenSummary {
		t.Fatalf("dry-run enter should reach summary, got %v", m.screen)
	}
	if len(fa.deleted) != 0 {
		t.Errorf("dry-run must not delete anything, got %d calls", len(fa.deleted))
	}
}

// TestViewRendersScreens verifies View() produces the expected content for each
// screen without needing a real terminal (rendering is a pure function of
// state).
func TestViewRendersScreens(t *testing.T) {
	fa := &fakeActions{}
	m := New(fa.build(groups(), nil, nil), scan.NewOptions(), false, false, false)

	// Settings screen shows the title and toggle labels.
	if s := m.View(); !contains(s, "Scan Settings") || !contains(s, "Include installed apps") {
		t.Errorf("settings view missing expected content:\n%s", s)
	}

	// Select screen shows section headers and sizes.
	m, _ = send(m, scanDoneMsg{res: scan.Result{Groups: groups()}, tools: nil})
	sv := m.View()
	for _, want := range []string{secLibrary, secDevCache, secToolchain, "Brave", "4.9M", "[ ]"} {
		if !contains(sv, want) {
			t.Errorf("select view missing %q:\n%s", want, sv)
		}
	}

	// Confirm screen warns about destructive deletion.
	m, _ = send(m, key("a"))
	m, _ = send(m, key("enter"))
	if cv := m.View(); !contains(cv, "Destructive") || !contains(cv, "UNINSTALLS") {
		t.Errorf("confirm view missing destructive warning:\n%s", cv)
	}
}

func contains(haystack, needle string) bool { return strings.Contains(haystack, needle) }

// TestToggleSetting exercises the settings toggles.
func TestToggleSetting(t *testing.T) {
	fa := &fakeActions{}
	m := New(fa.build(nil, nil, nil), scan.NewOptions(), false, false, false)
	if m.screen != screenSettings {
		t.Fatal("should start on settings")
	}
	// Move to "Dry-run" (index 4) and toggle it on.
	for i := 0; i < 4; i++ {
		m, _ = send(m, tea.KeyMsg{Type: tea.KeyDown})
	}
	m, _ = send(m, key("space"))
	if !m.dryRun {
		t.Error("space on the dry-run row should enable dry-run")
	}
	// Enter starts scanning.
	m, cmd := send(m, key("enter"))
	if m.screen != screenScanning || cmd == nil {
		t.Errorf("enter should start scanning with a command; screen=%v", m.screen)
	}
}
