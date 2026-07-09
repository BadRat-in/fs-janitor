// Package analytics turns raw filesystem signals into the FS Janitor
// "Maintenance Score" and the storage-health report that headlines the
// dashboard.
//
// The PRD calls for a single, glanceable number (0–100) plus a breakdown of
// where a machine is losing storage — reclaimable caches, stale Downloads,
// Desktop clutter, Time Machine snapshots — and how much space FS Janitor has
// recovered over its lifetime. This package computes exactly that from a set of
// pre-gathered Inputs.
//
// Scoring is deliberately pure: Compute takes numbers in and returns a Report,
// with no filesystem or clock access, so the thresholds and penalties are fully
// unit-tested. Collecting the Inputs (scanning, counting stale files) is the
// caller's job and lives in the wiring layer.
package analytics

import (
	"fmt"

	"github.com/BadRat-in/fs-janitor/internal/humanize"
)

// Inputs are the raw, already-measured signals the score is computed from.
type Inputs struct {
	ReclaimableKB   int64 // total cleanup candidate size from the scanner
	DevCacheKB      int64 // portion attributable to developer/build caches
	LibraryKB       int64 // portion attributable to app Library leftovers
	StaleDownloads  int   // files in ~/Downloads older than the stale threshold
	DesktopClutter  int   // items sitting on the Desktop
	Snapshots       int   // local Time Machine snapshots
	ActiveJobs      int   // enabled maintenance jobs
	LifetimeFreedKB int64 // total space reclaimed across all past runs
}

// Category is one line of the health breakdown: a named area with a status and,
// where relevant, how much it could give back.
type Category struct {
	Name          string
	Status        string // short human status, e.g. "Clean" or "18.4G"
	Detail        string // supplemental note, e.g. "742 files older than 90 days"
	ReclaimableKB int64
	Warn          bool // true when this area is dragging the score down
}

// Report is the computed maintenance snapshot rendered by the dashboard and the
// `fsj score` command.
type Report struct {
	Score           int
	Grade           string
	Categories      []Category
	PotentialKB     int64
	LifetimeFreedKB int64
	ActiveJobs      int
}

// Scoring thresholds. Penalties are capped per-category so no single area can
// zero the score on its own, and the total is clamped to [0,100].
const (
	maxReclaimPenalty  = 40
	maxDownloadPenalty = 20
	maxDesktopPenalty  = 10
	maxSnapPenalty     = 15
)

// Compute derives the maintenance Report from Inputs. The score starts at 100
// and loses points for reclaimable space (2 points per GB), stale Downloads (1
// point per 10 files), Desktop clutter (1 point per 10 items) and Time Machine
// snapshots (3 points each), each capped. The result is a stable, deterministic
// function of its inputs.
func Compute(in Inputs) Report {
	gb := float64(in.ReclaimableKB) / (1024 * 1024)
	penalty := 0
	penalty += capPenalty(int(gb*2), maxReclaimPenalty)
	penalty += capPenalty(in.StaleDownloads/10, maxDownloadPenalty)
	penalty += capPenalty(in.DesktopClutter/10, maxDesktopPenalty)
	penalty += capPenalty(in.Snapshots*3, maxSnapPenalty)

	score := 100 - penalty
	if score < 0 {
		score = 0
	}

	r := Report{
		Score:           score,
		Grade:           grade(score),
		PotentialKB:     in.ReclaimableKB,
		LifetimeFreedKB: in.LifetimeFreedKB,
		ActiveJobs:      in.ActiveJobs,
	}
	r.Categories = []Category{
		cacheCategory("Developer Cache", in.DevCacheKB),
		cacheCategory("App Leftovers", in.LibraryKB),
		countCategory("Downloads", in.StaleDownloads, "files older than 90 days"),
		countCategory("Desktop", in.DesktopClutter, "items on the Desktop"),
		snapCategory(in.Snapshots),
	}
	return r
}

// capPenalty clamps a raw penalty to [0,max].
func capPenalty(v, max int) int {
	if v < 0 {
		return 0
	}
	if v > max {
		return max
	}
	return v
}

// grade maps a score to a letter grade for the dashboard header.
func grade(score int) string {
	switch {
	case score >= 90:
		return "A"
	case score >= 75:
		return "B"
	case score >= 60:
		return "C"
	case score >= 40:
		return "D"
	default:
		return "F"
	}
}

// cacheCategory builds a size-based category (bigger = worse).
func cacheCategory(name string, kb int64) Category {
	if kb <= 0 {
		return Category{Name: name, Status: "Clean"}
	}
	return Category{
		Name:          name,
		Status:        humanize.Size(kb),
		ReclaimableKB: kb,
		Warn:          kb >= 1024*1024, // warn from ~1 GiB up
	}
}

// countCategory builds a count-based category (more items = worse).
func countCategory(name string, count int, noun string) Category {
	if count <= 0 {
		return Category{Name: name, Status: "Clean"}
	}
	return Category{
		Name:   name,
		Status: fmt.Sprintf("%d", count),
		Detail: fmt.Sprintf("%d %s", count, noun),
		Warn:   count > 0,
	}
}

// snapCategory summarizes Time Machine local snapshots.
func snapCategory(n int) Category {
	if n <= 0 {
		return Category{Name: "TM Snapshots", Status: "Clean"}
	}
	return Category{
		Name:   "TM Snapshots",
		Status: fmt.Sprintf("%d", n),
		Detail: fmt.Sprintf("%d local snapshot(s) reclaimable", n),
		Warn:   true,
	}
}
