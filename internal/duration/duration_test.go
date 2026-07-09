// Tests for the duration package: parsing compact human durations, formatting
// them back, whole-day extraction, and the full set of error conditions.
package duration

import (
	"testing"
	"time"
)

// TestParseValid exercises every unit, multi-unit combinations, whitespace
// handling, case-insensitivity, and the optional leading '+'.
func TestParseValid(t *testing.T) {
	const (
		day  = 24 * time.Hour
		week = 7 * day
		year = 365 * day
	)
	cases := []struct {
		in   string
		want time.Duration
	}{
		// Each single unit.
		{"45s", 45 * time.Second},
		{"90m", 90 * time.Minute},
		{"24h", 24 * time.Hour},
		{"30d", 30 * day},
		{"15d", 15 * day},
		{"2w", 2 * week},
		{"1y", year},
		// Multi-unit combos.
		{"1d12h", day + 12*time.Hour},
		{"1w3d", week + 3*day},
		{"1y2w3d4h5m6s", year + 2*week + 3*day + 4*time.Hour + 5*time.Minute + 6*time.Second},
		// Whitespace and sign handling.
		{"  30d ", 30 * day},
		{"+2w", 2 * week},
		{" +1d12h\t", day + 12*time.Hour},
		// Case-insensitivity.
		{"30D", 30 * day},
		{"1W3D", week + 3*day},
		{"24H", 24 * time.Hour},
		// Zero is a valid value.
		{"0s", 0},
	}
	for _, c := range cases {
		got, err := Parse(c.in)
		if err != nil {
			t.Errorf("Parse(%q) unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("Parse(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestParseErrors covers every documented failure mode.
func TestParseErrors(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"whitespace only", "   "},
		{"bare number", "30"},
		{"bare number with space", "  42 "},
		{"unknown unit", "5x"},
		{"negative", "-30d"},
		{"garbage", "abc"},
		{"fractional", "1.5h"},
		{"leading unit", "d"},
		{"sign only", "+"},
		{"trailing digits", "1d30"},
		{"double sign", "++1d"},
	}
	for _, c := range cases {
		if _, err := Parse(c.in); err == nil {
			t.Errorf("%s: Parse(%q) expected error, got nil", c.name, c.in)
		}
	}
}

// TestFormat checks compact rendering, unit preference, zero, sub-second
// rounding, and negatives.
func TestFormat(t *testing.T) {
	const (
		day  = 24 * time.Hour
		week = 7 * day
		year = 365 * day
	)
	cases := []struct {
		in   time.Duration
		want string
	}{
		{0, "0s"},
		{30 * day, "30d"},
		{36 * time.Hour, "1d12h"},
		{week, "1w"},
		{year, "1y"},
		{45 * time.Second, "45s"},
		{90 * time.Minute, "1h30m"},
		{14 * day, "2w"},                           // exact week multiple.
		{week + 3*day, "10d"},                      // 10 days: weeks don't divide, stays days.
		{7*day + 12*time.Hour, "7d12h"},            // sub-day remainder: no partial week.
		{500 * time.Millisecond, "0s"},             // sub-second rounds down.
		{time.Second + 500*time.Millisecond, "1s"}, // remainder dropped.
		{-36 * time.Hour, "-1d12h"},
	}
	for _, c := range cases {
		if got := Format(c.in); got != c.want {
			t.Errorf("Format(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestRoundTrip verifies Parse->Format is stable for canonical compact inputs.
func TestRoundTrip(t *testing.T) {
	// Canonical (self-round-tripping) forms only: "90m" formats to "1h30m" and
	// "1w3d" to "10d", so those are covered separately in TestFormat.
	inputs := []string{"45s", "1h30m", "1d12h", "30d", "15d", "1w", "2w", "1y", "0s"}
	for _, in := range inputs {
		d, err := Parse(in)
		if err != nil {
			t.Errorf("Parse(%q) unexpected error: %v", in, err)
			continue
		}
		if got := Format(d); got != in {
			t.Errorf("round-trip %q: Format(Parse(%q)) = %q", in, in, got)
		}
	}
}

// TestDays checks whole-day extraction including flooring behaviour.
func TestDays(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want int
	}{
		{0, 0},
		{23 * time.Hour, 0},
		{24 * time.Hour, 1},
		{36 * time.Hour, 1},
		{48 * time.Hour, 2},
		{30 * 24 * time.Hour, 30},
		{-36 * time.Hour, -1},
	}
	for _, c := range cases {
		if got := Days(c.in); got != c.want {
			t.Errorf("Days(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}
