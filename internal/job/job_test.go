package job

import (
	"testing"
	"time"
)

func fixedTime() time.Time { return time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC) }

func TestNewExpireDefaults(t *testing.T) {
	now := fixedTime()
	j := NewExpire("/tmp/x.zip", 15*24*time.Hour, "", now)
	if j.Kind != KindExpire {
		t.Fatalf("kind = %q", j.Kind)
	}
	if j.Action != ActionTrash {
		t.Errorf("action = %q, want default trash", j.Action)
	}
	if j.Basis != BasisNow {
		t.Errorf("basis = %q, want now", j.Basis)
	}
	if !j.DueAt.Equal(now.Add(15 * 24 * time.Hour)) {
		t.Errorf("due = %v", j.DueAt)
	}
	if j.Name != "x.zip" {
		t.Errorf("name = %q", j.Name)
	}
	if !j.Enabled {
		t.Error("expected enabled")
	}
}

func TestNewWatchDefaults(t *testing.T) {
	j := NewWatch("/tmp/dl", 30*24*time.Hour, "", ActionDelete, fixedTime())
	if j.Basis != BasisModified {
		t.Errorf("basis = %q, want default modified", j.Basis)
	}
	if j.Action != ActionDelete {
		t.Errorf("action = %q", j.Action)
	}
	if j.Name != "dl" {
		t.Errorf("name = %q", j.Name)
	}
}

func TestValidate(t *testing.T) {
	now := fixedTime()
	ok := NewExpire("/tmp/a", time.Hour, ActionTrash, now)
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid job rejected: %v", err)
	}
	cases := map[string]Job{
		"no path":      {Kind: KindExpire, Action: ActionTrash, Basis: BasisNow, After: time.Hour, DueAt: now},
		"bad kind":     {Path: "/x", Kind: "nope", Action: ActionTrash, Basis: BasisNow, After: time.Hour, DueAt: now},
		"bad action":   {Path: "/x", Kind: KindWatch, Action: "nope", Basis: BasisModified, After: time.Hour},
		"bad basis":    {Path: "/x", Kind: KindWatch, Action: ActionTrash, Basis: "nope", After: time.Hour},
		"zero after":   {Path: "/x", Kind: KindWatch, Action: ActionTrash, Basis: BasisModified, After: 0},
		"expire nodue": {Path: "/x", Kind: KindExpire, Action: ActionTrash, Basis: BasisNow, After: time.Hour},
	}
	for name, j := range cases {
		if err := j.Validate(); err == nil {
			t.Errorf("%s: expected validation error", name)
		}
	}
}

func TestDue(t *testing.T) {
	now := fixedTime()
	j := NewExpire("/x", time.Hour, ActionTrash, now)
	if j.Due(now) {
		t.Error("should not be due at creation")
	}
	if !j.Due(now.Add(time.Hour)) {
		t.Error("should be due at due time")
	}
	if !j.Due(now.Add(2 * time.Hour)) {
		t.Error("should be due after due time")
	}
	w := NewWatch("/x", time.Hour, BasisModified, ActionTrash, now)
	if w.Due(now.Add(100 * time.Hour)) {
		t.Error("watch jobs are never 'due'")
	}
}

func TestDescribe(t *testing.T) {
	now := fixedTime()
	if got := NewExpire("/tmp/a", 24*time.Hour, ActionTrash, now).Describe(); got == "" {
		t.Error("empty expire describe")
	}
	w := NewWatch("/tmp/dl", 30*24*time.Hour, BasisModified, ActionDelete, now)
	w.MinSizeKB = 2048
	if got := w.Describe(); got == "" {
		t.Error("empty watch describe")
	}
}
