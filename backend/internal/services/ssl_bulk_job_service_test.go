package services

import (
	"testing"
)

// TestComputeBulkJobProgress_StandardCases locks the
// (success+failed)/total*100 contract the SSLBulkJob.progress field
// records — the frontend's progress bar reads this value directly,
// so a future edit that quietly changes the rounding behaviour
// would slide the bar at a different rate than the row table fills.
func TestComputeBulkJobProgress_StandardCases(t *testing.T) {
	cases := []struct {
		name                     string
		success, failed, total   int
		want                     int
	}{
		{"empty job stays at 0", 0, 0, 0, 0},
		{"nothing processed yet", 0, 0, 27, 0},
		{"first row succeeded on a 27-domain batch", 1, 0, 27, 3},   // 100/27 ≈ 3.7 → floor 3
		{"first row failed on a 27-domain batch", 0, 1, 27, 3},
		{"halfway through", 13, 1, 27, 51},                            // (14/27)*100 = 51.85 → 51
		{"every row processed", 25, 2, 27, 100},
		{"single-domain success", 1, 0, 1, 100},
		{"single-domain failure", 0, 1, 1, 100},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ComputeBulkJobProgress(c.success, c.failed, c.total)
			if got != c.want {
				t.Errorf("ComputeBulkJobProgress(s=%d, f=%d, t=%d) = %d, want %d",
					c.success, c.failed, c.total, got, c.want)
			}
		})
	}
}

// TestComputeBulkJobProgress_ClampsToZeroAndHundred asserts the
// defensive bounds — a caller that passes a negative success/failed
// count (shouldn't happen, but the function is pure and exported)
// can't produce a negative progress, and overflow past 100 (also
// shouldn't happen unless total is mis-stamped, but again — pure
// function) clamps cleanly.
func TestComputeBulkJobProgress_ClampsToZeroAndHundred(t *testing.T) {
	if got := ComputeBulkJobProgress(-5, 0, 10); got != 0 {
		t.Errorf("negative success should clamp to 0, got %d", got)
	}
	if got := ComputeBulkJobProgress(50, 50, 10); got != 100 {
		t.Errorf("over-100 raw value should clamp to 100, got %d", got)
	}
	// Total of 0 with done > 0 (degenerate caller state) — return 0
	// rather than divide-by-zero.
	if got := ComputeBulkJobProgress(1, 0, 0); got != 0 {
		t.Errorf("total of 0 should return 0 (no work, no progress), got %d", got)
	}
}

// TestComputeBulkJobProgress_NoNegativeFromDoneOverTotal is a
// regression guard against a future "use signed math everywhere"
// edit. Done > Total shouldn't happen in healthy state, but if it
// did (e.g. a duplicate row outcome write), the function should
// clamp to 100 rather than report 150%.
func TestComputeBulkJobProgress_NoNegativeFromDoneOverTotal(t *testing.T) {
	if got := ComputeBulkJobProgress(15, 0, 10); got != 100 {
		t.Errorf("done > total should clamp to 100, got %d", got)
	}
}
