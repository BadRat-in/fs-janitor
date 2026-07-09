package analytics

import "testing"

func TestPerfectMachineScores100(t *testing.T) {
	r := Compute(Inputs{})
	if r.Score != 100 || r.Grade != "A" {
		t.Fatalf("clean machine: score=%d grade=%s", r.Score, r.Grade)
	}
	for _, c := range r.Categories {
		if c.Warn {
			t.Errorf("clean machine should have no warnings, got %q", c.Name)
		}
		if c.Status != "Clean" {
			t.Errorf("category %q status = %q, want Clean", c.Name, c.Status)
		}
	}
}

func TestReclaimablePenaltyCap(t *testing.T) {
	// 100 GB reclaimable would be 200 raw points; must cap the category at 40.
	huge := int64(100) * 1024 * 1024
	r := Compute(Inputs{ReclaimableKB: huge, DevCacheKB: huge})
	if r.Score != 60 {
		t.Errorf("score = %d, want 100-40=60 (reclaim penalty capped)", r.Score)
	}
}

func TestWorstCaseIsFloored(t *testing.T) {
	// Every category penalty is capped, so the worst reachable score is
	// 100-(40+20+10+15)=15 — still a solid F, and never negative.
	r := Compute(Inputs{
		ReclaimableKB:  int64(1000) * 1024 * 1024,
		StaleDownloads: 10_000,
		DesktopClutter: 10_000,
		Snapshots:      100,
	})
	if r.Score != 15 {
		t.Errorf("score = %d, want floored 15", r.Score)
	}
	if r.Grade != "F" {
		t.Errorf("grade = %s, want F", r.Grade)
	}
}

func TestDownloadsPenaltyAndDetail(t *testing.T) {
	r := Compute(Inputs{StaleDownloads: 50}) // 50/10 = 5 points
	if r.Score != 95 {
		t.Errorf("score = %d, want 95", r.Score)
	}
	var dl Category
	for _, c := range r.Categories {
		if c.Name == "Downloads" {
			dl = c
		}
	}
	if !dl.Warn || dl.Status != "50" || dl.Detail == "" {
		t.Errorf("downloads category wrong: %+v", dl)
	}
}

func TestGrades(t *testing.T) {
	gb := func(n int64) int64 { return n * 1024 * 1024 }
	cases := []struct {
		name  string
		in    Inputs
		grade string
	}{
		{"A", Inputs{}, "A"},                                                                 // 100
		{"B", Inputs{ReclaimableKB: gb(6)}, "B"},                                             // -12 → 88
		{"C", Inputs{ReclaimableKB: gb(13)}, "C"},                                            // -26 → 74
		{"D", Inputs{ReclaimableKB: gb(100), StaleDownloads: 100}, "D"},                      // -40-10 → 50
		{"F", Inputs{ReclaimableKB: gb(100), StaleDownloads: 300, DesktopClutter: 100}, "F"}, // -40-20-10 → 30
	}
	for _, c := range cases {
		r := Compute(c.in)
		if r.Grade != c.grade {
			t.Errorf("%s: grade %s, want %s (score %d)", c.name, r.Grade, c.grade, r.Score)
		}
	}
}

func TestReportCarriesInputs(t *testing.T) {
	r := Compute(Inputs{ActiveJobs: 3, LifetimeFreedKB: 12345, ReclaimableKB: 100})
	if r.ActiveJobs != 3 || r.LifetimeFreedKB != 12345 || r.PotentialKB != 100 {
		t.Errorf("report passthrough wrong: %+v", r)
	}
}
