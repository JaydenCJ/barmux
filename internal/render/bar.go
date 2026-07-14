// Package render turns dashboard state into terminal output. It has two
// personalities: Frame builds a full-screen-region ANSI frame for live
// TTYs, and Plain streams milestone lines for CI logs and pipes. Both are
// pure functions of state (plus explicit width/color options), so every
// pixel of output is unit-testable.
package render

import "strings"

// Glyphs used by determinate bars. Chosen to match what modern terminals
// render at full cell width; ASCII fallback is available via Options.ASCII.
const (
	fillRune  = "█"
	emptyRune = "░"
)

// SpinnerFrames are the phases of the indeterminate-task spinner. The
// renderer picks a frame by (phase % len); callers advance phase on each
// repaint, tests pin it.
var SpinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// asciiSpinnerFrames is the ASCII fallback spinner.
var asciiSpinnerFrames = []string{"|", "/", "-", "\\"}

// Bar renders a determinate progress bar of the given cell width.
// current is clamped to [0,total]; width < 1 yields an empty string.
func Bar(current, total int64, width int, ascii bool) string {
	if width < 1 {
		return ""
	}
	if total < 1 {
		total = 1
	}
	if current < 0 {
		current = 0
	}
	if current > total {
		current = total
	}
	filled := int(current * int64(width) / total)
	// current*width can overflow int64 for absurd-but-legal counts; the
	// clamp keeps strings.Repeat from panicking on a negative count.
	if filled < 0 {
		filled = 0
	} else if filled > width {
		filled = width
	}
	fill, empty := fillRune, emptyRune
	if ascii {
		fill, empty = "#", "-"
	}
	return strings.Repeat(fill, filled) + strings.Repeat(empty, width-filled)
}

// Spinner returns the spinner glyph for a repaint phase.
func Spinner(phase int, ascii bool) string {
	frames := SpinnerFrames
	if ascii {
		frames = asciiSpinnerFrames
	}
	if phase < 0 {
		phase = -phase
	}
	return frames[phase%len(frames)]
}

// Truncate shortens s to at most max runes, appending "…" ("..." in ASCII
// mode) when it had to cut. Widths are counted in runes, not terminal
// cells; East-Asian double-width text may still overflow by design — the
// protocol is byte-bounded, and cell-width tables are out of scope for
// a zero-dependency 0.1.0 (see README limitations).
func Truncate(s string, max int, ascii bool) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	ellipsis := "…"
	if ascii {
		ellipsis = "..."
	}
	ell := []rune(ellipsis)
	if max < len(ell) {
		return string(runes[:max]) // no room for an ellipsis: hard cut
	}
	return string(runes[:max-len(ell)]) + ellipsis
}

// Pad right-pads s with spaces to exactly max runes, truncating first.
func Pad(s string, max int, ascii bool) string {
	s = Truncate(s, max, ascii)
	if n := max - len([]rune(s)); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}
