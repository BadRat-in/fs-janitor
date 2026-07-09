package humanize

import "testing"

// TestSize verifies each unit boundary and the zero/negative guard, matching
// the reference script's awk formatter exactly.
func TestSize(t *testing.T) {
	cases := []struct {
		kb   int64
		want string
	}{
		{-5, "0K"},
		{0, "0K"},
		{1, "1.0K"},
		{512, "512.0K"},
		{1023, "1023.0K"},
		{1024, "1.0M"},       // exact MiB boundary
		{1536, "1.5M"},       // 1.5 MiB
		{1048575, "1024.0M"}, // just below GiB boundary
		{1048576, "1.0G"},    // exact GiB boundary
		{528896, "516.5M"},   // Cargo-cache-sized value
		{10066329, "9.6G"},   // Chrome-sized value (~9.6 GiB)
	}
	for _, c := range cases {
		if got := Size(c.kb); got != c.want {
			t.Errorf("Size(%d) = %q, want %q", c.kb, got, c.want)
		}
	}
}
