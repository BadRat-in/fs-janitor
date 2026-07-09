// Package humanize formats byte/kilobyte quantities for display in the CLI and
// TUI. It exists so size rendering is defined once and unit-tested, rather than
// duplicated at each call site (the reference script open-coded this in awk).
package humanize

import "fmt"

// Size formats a size given in kilobytes into a compact human-readable string
// using binary (1024-based) units, matching the reference script's output:
//
//	0 or less  -> "0K"
//	>= 1 GiB   -> "%.1fG"  (kb >= 1048576)
//	>= 1 MiB   -> "%.1fM"  (kb >= 1024)
//	otherwise  -> "%.1fK"
//
// Kilobytes are the unit because that is what `du -sk` reports, which is how
// CleanX measures every path.
func Size(kb int64) string {
	switch {
	case kb <= 0:
		return "0K"
	case kb >= 1048576:
		return fmt.Sprintf("%.1fG", float64(kb)/1048576)
	case kb >= 1024:
		return fmt.Sprintf("%.1fM", float64(kb)/1024)
	default:
		return fmt.Sprintf("%.1fK", float64(kb))
	}
}
