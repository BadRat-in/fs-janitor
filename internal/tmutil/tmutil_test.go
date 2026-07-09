package tmutil

import (
	"errors"
	"testing"
)

// TestListSnapshotsFiltersHeader verifies the "Snapshots for disk /:" header is
// dropped and only real snapshot identifiers are returned.
func TestListSnapshotsFiltersHeader(t *testing.T) {
	out := "Snapshots for disk /:\n" +
		"com.apple.TimeMachine.2026-07-01-120000.local\n" +
		"com.apple.TimeMachine.2026-07-02-080000.local\n"
	run := func(name string, args ...string) (string, error) { return out, nil }

	snaps, err := ListSnapshots(run)
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 2 {
		t.Fatalf("expected 2 snapshots, got %d: %v", len(snaps), snaps)
	}
	for _, s := range snaps {
		if s == "Snapshots for disk /:" {
			t.Error("header line must not be treated as a snapshot")
		}
	}
}

// TestListSnapshotsEmpty verifies the no-snapshot case (header only).
func TestListSnapshotsEmpty(t *testing.T) {
	run := func(name string, args ...string) (string, error) {
		return "Snapshots for disk /:\n", nil
	}
	snaps, err := ListSnapshots(run)
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 0 {
		t.Errorf("expected 0 snapshots, got %v", snaps)
	}
}

// TestDeleteSnapshot passes the identifier through to the runner.
func TestDeleteSnapshot(t *testing.T) {
	var gotArgs []string
	run := func(name string, args ...string) (string, error) {
		gotArgs = args
		return "", nil
	}
	if err := DeleteSnapshot(run, "com.apple.TimeMachine.2026-07-01-120000.local"); err != nil {
		t.Fatal(err)
	}
	if len(gotArgs) != 2 || gotArgs[0] != "deletelocalsnapshots" {
		t.Errorf("unexpected args: %v", gotArgs)
	}

	failRun := func(name string, args ...string) (string, error) { return "", errors.New("boom") }
	if err := DeleteSnapshot(failRun, "x"); err == nil {
		t.Error("expected error to propagate")
	}
}
