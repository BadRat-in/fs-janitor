// Package duration parses and formats the compact, human-friendly retention and
// expiry durations that fs-janitor jobs are configured with (for example
// "expire after 30d" or "keep for 2w"). Users and config files express age
// thresholds in short forms like "30d", "24h", "1w3d", and the janitor needs to
// turn those into time.Duration values to compare against file ages, then render
// computed durations back into the same compact form for display.
//
// It exists so this parsing/formatting is defined once and unit-tested, instead
// of being re-implemented (and subtly disagreeing) at every call site that reads
// a retention setting. The vocabulary is intentionally small and calendar-free:
// days, weeks, and years are fixed multiples of hours (a day is exactly 24h, a
// week 7 days, a year 365 days), which keeps thresholds predictable and avoids
// dragging in time-zone or leap-handling concerns that do not matter for "is
// this file older than N days" decisions.
package duration

import (
	"fmt"
	"strings"
	"time"
)

// unitDurations maps each single-letter unit to the time.Duration it represents.
// Days, weeks, and years are deliberately fixed multiples rather than calendar
// aware: a day is 24h, a week 7 days, and a year 365 days.
var unitDurations = map[byte]time.Duration{
	's': time.Second,
	'm': time.Minute,
	'h': time.Hour,
	'd': 24 * time.Hour,
	'w': 7 * 24 * time.Hour,
	'y': 365 * 24 * time.Hour,
}

// Parse converts a compact human duration string into a time.Duration.
//
// It exists because fs-janitor retention/expiry values are written in short
// forms like "30d" or "1w3d" (in config files and on the command line) and must
// be turned into a time.Duration to compare against file ages.
//
// Accepted syntax:
//   - One or more "<integer><unit>" groups concatenated, e.g. "90m", "1d12h",
//     "1w3d". Groups are summed.
//   - Units (case-insensitive): s=second, m=minute, h=hour, d=day(24h),
//     w=week(7d), y=year(365d).
//   - Surrounding whitespace is ignored and a single optional leading '+' is
//     allowed, e.g. "  +2w ".
//   - Only whole-integer counts are allowed; fractional values like "1.5h" are
//     rejected.
//
// Parameters:
//   - s: the duration string to parse.
//
// Returns the parsed time.Duration and a nil error on success. On failure it
// returns a zero duration and a descriptive error created with fmt.Errorf.
//
// Edge cases and errors:
//   - Empty or whitespace-only input is an error.
//   - A bare integer with no unit (e.g. "30") is rejected as ambiguous.
//   - Negative values (a leading '-') are rejected; durations are non-negative.
//   - Unknown units, missing digits, fractional numbers, and any other
//     malformed input produce an error.
func Parse(s string) (time.Duration, error) {
	// Normalise: trim surrounding whitespace and lower-case so units are
	// matched case-insensitively.
	orig := s
	s = strings.ToLower(strings.TrimSpace(s))

	if s == "" {
		return 0, fmt.Errorf("duration: empty string")
	}

	// A single optional leading '+' is allowed; a leading '-' (or any other
	// sign) is not, because retention durations are always non-negative.
	if s[0] == '+' {
		s = s[1:]
		if s == "" {
			return 0, fmt.Errorf("duration: %q has no value after sign", orig)
		}
	} else if s[0] == '-' {
		return 0, fmt.Errorf("duration: %q is negative; durations must be non-negative", orig)
	}

	var total time.Duration
	i := 0
	n := len(s)
	for i < n {
		// Parse the run of digits forming this group's count.
		start := i
		for i < n && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i == start {
			// No digits where a number was expected: either a stray unit
			// letter or garbage such as a fractional point.
			return 0, fmt.Errorf("duration: %q is malformed at %q", orig, s[i:])
		}

		// Accumulate the integer value, guarding against overflow.
		var value int64
		for _, c := range s[start:i] {
			value = value*10 + int64(c-'0')
			if value < 0 {
				return 0, fmt.Errorf("duration: %q is too large", orig)
			}
		}

		if i >= n {
			// Digits ran to the end with no trailing unit: this is the
			// ambiguous "bare number" case (e.g. "30").
			return 0, fmt.Errorf("duration: %q is missing a unit (use s, m, h, d, w or y)", orig)
		}

		// The next byte must be a known unit letter.
		unit := s[i]
		ud, ok := unitDurations[unit]
		if !ok {
			return 0, fmt.Errorf("duration: %q has unknown unit %q (use s, m, h, d, w or y)", orig, string(unit))
		}
		i++

		// Add this group, checking that the multiplication and sum do not
		// overflow the int64 nanosecond counter.
		add := time.Duration(value) * ud
		if value != 0 && add/ud != time.Duration(value) {
			return 0, fmt.Errorf("duration: %q is too large", orig)
		}
		total += add
		if total < 0 {
			return 0, fmt.Errorf("duration: %q is too large", orig)
		}
	}

	return total, nil
}

// exactDayUnits are the day-or-larger units, largest first, used when a
// duration is an exact multiple of a whole day. The largest unit that divides
// the duration evenly is chosen so a clean value renders as a single unit
// (e.g. 7 days -> "1w", 30 days -> "30d" because weeks do not divide 30).
var exactDayUnits = []struct {
	suffix string
	size   time.Duration
}{
	{"y", 365 * 24 * time.Hour},
	{"w", 7 * 24 * time.Hour},
	{"d", 24 * time.Hour},
}

// subDayUnits are the units, largest first, used to greedily decompose a
// duration that has a sub-day remainder. Weeks and years are intentionally
// excluded here: they appear only for exact week/year multiples (via
// exactDayUnits), which keeps output like "1d12h" and "7d12h" rather than
// splitting into partial weeks.
var subDayUnits = []struct {
	suffix string
	size   time.Duration
}{
	{"d", 24 * time.Hour},
	{"h", time.Hour},
	{"m", time.Minute},
	{"s", time.Second},
}

// Format renders a time.Duration back into the compact form Parse accepts.
//
// It exists so computed durations (for example "this file expires in ...") are
// displayed in the same short vocabulary users type, keeping input and output
// symmetrical.
//
// Behaviour:
//   - Larger units are preferred, so 30*24h renders as "30d" and 7*24h as "1w".
//   - An exact multiple of a whole day collapses to the largest day-or-larger
//     unit that divides it evenly: 7 days -> "1w", 14 days -> "2w", but 30 days
//     -> "30d" (weeks do not divide 30 evenly).
//   - A duration with a sub-day remainder is decomposed largest-first over days,
//     hours, minutes and seconds, e.g. 36h -> "1d12h" and 90m -> "1h30m". Weeks
//     and years only appear for exact week/year multiples, never as partial
//     leading components.
//   - Zero-valued components are omitted.
//   - The duration is handled at second granularity: any sub-second remainder is
//     dropped (rounded down toward zero).
//
// Parameters:
//   - d: the duration to render.
//
// Returns the compact string representation.
//
// Edge cases:
//   - A zero duration returns "0s".
//   - A negative duration is rendered with a leading '-' followed by the
//     formatting of its magnitude (Format is primarily meant for the
//     non-negative durations Parse produces, but this keeps output sensible).
func Format(d time.Duration) string {
	if d == 0 {
		return "0s"
	}

	// Handle negative durations by formatting the magnitude with a '-' prefix.
	neg := d < 0
	if neg {
		d = -d
	}

	// Work at whole-second granularity, discarding any sub-second remainder.
	d = d - (d % time.Second)
	if d == 0 {
		// The value was non-zero but smaller than a second; report "0s".
		return "0s"
	}

	var b strings.Builder
	if neg {
		b.WriteByte('-')
	}

	const day = 24 * time.Hour
	// Exact whole-day multiples collapse to a single day-or-larger unit.
	if d%day == 0 {
		for _, u := range exactDayUnits {
			if d%u.size == 0 {
				fmt.Fprintf(&b, "%d%s", d/u.size, u.suffix)
				return b.String()
			}
		}
	}

	// Otherwise decompose greedily over day/hour/minute/second, largest first,
	// omitting any component whose count is zero.
	for _, u := range subDayUnits {
		if d >= u.size {
			count := d / u.size
			d -= count * u.size
			fmt.Fprintf(&b, "%d%s", count, u.suffix)
		}
	}
	return b.String()
}

// Days returns the number of whole days contained in the duration.
//
// It exists as a convenience for callers that reason about retention purely in
// days (the most common fs-janitor threshold unit) and want the floored day
// count without repeating the 24h arithmetic.
//
// Parameters:
//   - d: the duration to measure.
//
// Returns the whole-day count (e.g. 36h -> 1, 23h -> 0, 48h -> 2). For the
// non-negative durations fs-janitor normally uses this is a plain floor;
// negative durations truncate toward zero per Go's integer division (e.g.
// -36h -> -1).
func Days(d time.Duration) int {
	return int(d / (24 * time.Hour))
}
