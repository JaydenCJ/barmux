// Tests for the bar/spinner/text primitives. These are the cells every
// frame is built from, so widths and clamping are asserted exactly.
package render

import (
	"math"
	"strings"
	"testing"
)

func TestBarExactWidths(t *testing.T) {
	if got := Bar(5, 10, 10, false); got != "█████░░░░░" {
		t.Fatalf("half bar: %q", got)
	}
	if got := Bar(0, 10, 8, false); got != strings.Repeat("░", 8) {
		t.Fatalf("empty bar: %q", got)
	}
	if got := Bar(10, 10, 8, false); got != strings.Repeat("█", 8) {
		t.Fatalf("full bar: %q", got)
	}
}

func TestBarClampsOutOfRangeInput(t *testing.T) {
	// Children can over-tick or send garbage; the renderer never panics
	// and never draws outside the cell budget.
	if got := Bar(999, 10, 6, false); got != strings.Repeat("█", 6) {
		t.Fatalf("overshoot: %q", got)
	}
	if got := Bar(-3, 10, 6, false); got != strings.Repeat("░", 6) {
		t.Fatalf("negative: %q", got)
	}
	if got := Bar(1, 0, 6, false); len([]rune(got)) != 6 {
		t.Fatalf("zero total must not divide by zero: %q", got)
	}
	if got := Bar(5, 10, 0, false); got != "" {
		t.Fatalf("zero width: %q", got)
	}
}

func TestBarHugeCountsDoNotOverflow(t *testing.T) {
	// current*width overflows int64 for absurd-but-legal counts; the bar
	// must clamp and keep its cell budget, never panic, whatever a child
	// claims over the pipe.
	got := Bar(math.MaxInt64-1, math.MaxInt64, 10, false)
	if n := len([]rune(got)); n != 10 {
		t.Fatalf("overflowing counts broke the width: %d cells (%q)", n, got)
	}
}

func TestBarASCIIMode(t *testing.T) {
	if got := Bar(2, 4, 4, true); got != "##--" {
		t.Fatalf("ascii bar: %q", got)
	}
}

func TestBarPartialProgressFloors(t *testing.T) {
	// 1/3 of 10 cells floors to 3 filled cells — never rounds past truth.
	got := Bar(1, 3, 10, false)
	if strings.Count(got, "█") != 3 {
		t.Fatalf("floor fill: %q", got)
	}
}

func TestSpinnerCyclesFramesInBothModes(t *testing.T) {
	if Spinner(0, false) != SpinnerFrames[0] {
		t.Fatal("phase 0")
	}
	if Spinner(len(SpinnerFrames), false) != SpinnerFrames[0] {
		t.Fatal("phase must wrap")
	}
	if Spinner(-1, false) == "" {
		t.Fatal("negative phase must not panic")
	}
	for phase := 0; phase < 8; phase++ {
		s := Spinner(phase, true)
		if len(s) != 1 || s[0] > 127 {
			t.Fatalf("ascii spinner frame %d: %q", phase, s)
		}
	}
}

func TestTruncateBudgetsAndEllipsis(t *testing.T) {
	if got := Truncate("hello", 10, false); got != "hello" {
		t.Fatalf("no-op truncate: %q", got)
	}
	got := Truncate("compiling something long", 10, false)
	if got != "compiling…" || len([]rune(got)) != 10 {
		t.Fatalf("truncate: %q", got)
	}
	if got := Truncate("compiling something long", 10, true); got != "compili..." {
		t.Fatalf("ascii truncate: %q", got)
	}
}

func TestTruncateRuneBoundariesAndTinyBudgets(t *testing.T) {
	// Multibyte labels (日本語ラベル) must be cut at rune boundaries,
	// never producing invalid UTF-8.
	if got := Truncate("ビルド中です", 4, false); got != "ビルド…" {
		t.Fatalf("rune truncate: %q", got)
	}
	if got := Truncate("abc", 0, false); got != "" {
		t.Fatalf("zero budget: %q", got)
	}
	if got := Truncate("abcdef", 1, false); got != "…" {
		t.Fatalf("budget 1: %q", got)
	}
	// An ASCII budget smaller than "..." hard-cuts instead of overflowing.
	if got := Truncate("abcdef", 2, true); got != "ab" {
		t.Fatalf("ascii budget 2: %q", got)
	}
}

func TestPadPadsAndTruncates(t *testing.T) {
	if got := Pad("ab", 5, false); got != "ab   " {
		t.Fatalf("pad: %q", got)
	}
	if got := Pad("abcdefgh", 5, false); len([]rune(got)) != 5 {
		t.Fatalf("pad must truncate first: %q", got)
	}
}
