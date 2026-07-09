// helpers.go holds small formatting helpers shared across the TUI screens.
package tui

import (
	"fmt"
	"strconv"

	"github.com/BadRat-in/fs-janitor/internal/humanize"
)

// itoa is a terse int→string used throughout the views.
func itoa(n int) string { return strconv.Itoa(n) }

// statusRun formats the footer status after a "run due jobs" action.
func statusRun(count int, freedKB int64) string {
	if count == 0 {
		return "No jobs were due."
	}
	return fmt.Sprintf("Ran %d job(s), reclaimed %s", count, humanize.Size(freedKB))
}

// padRight pads s with spaces to width (used for aligned columns); if s is
// already wider it is returned unchanged.
func padRight(s string, width int) string {
	for len(s) < width {
		s += " "
	}
	return s
}
