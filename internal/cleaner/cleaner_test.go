package cleaner

import (
	"errors"
	"testing"
)

// TestDeletePathsFreedAccounting verifies that only successfully deleted paths
// contribute to the freed total, and per-path outcomes are recorded.
func TestDeletePathsFreedAccounting(t *testing.T) {
	sizes := map[string]int64{
		"/a": 100,
		"/b": 250,
		"/c": 999, // will fail to delete -> not counted
	}
	size := func(p string) int64 { return sizes[p] }
	remove := func(p string) error {
		if p == "/c" {
			return errors.New("permission denied")
		}
		return nil
	}

	res := DeletePaths([]string{"/a", "/b", "/c", ""}, size, remove)

	if res.FreedKB != 350 {
		t.Errorf("FreedKB = %d, want 350 (only /a + /b succeed)", res.FreedKB)
	}
	if len(res.Paths) != 3 {
		t.Fatalf("expected 3 path results (empty skipped), got %d", len(res.Paths))
	}
	byPath := map[string]PathResult{}
	for _, pr := range res.Paths {
		byPath[pr.Path] = pr
	}
	if !byPath["/a"].Deleted || !byPath["/b"].Deleted {
		t.Error("/a and /b should be marked deleted")
	}
	if byPath["/c"].Deleted || byPath["/c"].Err == nil {
		t.Error("/c should be marked not-deleted with an error")
	}
}
