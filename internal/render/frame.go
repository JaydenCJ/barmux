// TTY frame building: one line per task plus a summary footer, redrawn in
// place by the live loop with ANSI cursor movement.
package render

import (
	"fmt"
	"strings"
	"time"

	"github.com/JaydenCJ/barmux/internal/state"
)

// Options controls frame and plain rendering.
type Options struct {
	Width int  // terminal width in cells; <= 0 means DefaultWidth
	Color bool // emit ANSI SGR colors
	ASCII bool // ASCII-only glyphs (no block/braille/ellipsis runes)
	Step  int  // Plain: percent step between milestones; <= 0 means 10
}

// DefaultWidth is assumed when the terminal width is unknown.
const DefaultWidth = 80

// ANSI SGR fragments; colors are standard 8-color codes so they survive
// every terminal palette.
const (
	sgrReset  = "\x1b[0m"
	sgrGreen  = "\x1b[32m"
	sgrRed    = "\x1b[31m"
	sgrCyan   = "\x1b[36m"
	sgrDim    = "\x1b[2m"
	sgrBold   = "\x1b[1m"
	sgrYellow = "\x1b[33m"
)

func (o Options) width() int {
	if o.Width <= 0 {
		return DefaultWidth
	}
	if o.Width < 20 {
		return 20 // below this a bar is meaningless; clamp instead of panic
	}
	return o.Width
}

func (o Options) paint(code, s string) string {
	if !o.Color || s == "" {
		return s
	}
	return code + s + sgrReset
}

// labelWidth is the fixed cell budget for task labels in a frame.
func labelWidth(total int) int {
	w := total / 4
	if w < 10 {
		w = 10
	}
	if w > 24 {
		w = 24
	}
	return w
}

// statusGlyph returns the leading marker for a task line.
func statusGlyph(t *state.Task, phase int, o Options) string {
	switch t.Status {
	case state.Done:
		if o.ASCII {
			return o.paint(sgrGreen, "+")
		}
		return o.paint(sgrGreen, "✔")
	case state.Failed:
		if o.ASCII {
			return o.paint(sgrRed, "x")
		}
		return o.paint(sgrRed, "✘")
	default:
		return o.paint(sgrCyan, Spinner(phase, o.ASCII))
	}
}

// elapsed formats a task's running time as m:ss (or h:mm:ss past an hour).
func elapsed(t *state.Task, now time.Time) string {
	end := now
	if !t.FinishedAt.IsZero() {
		end = t.FinishedAt
	}
	d := end.Sub(t.StartedAt)
	if d < 0 {
		d = 0
	}
	s := int(d / time.Second)
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, (s%3600)/60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// TaskLine renders one task row at the given width. Layout:
//
//	✔ label........ [██████░░░░]  62% (31/50)  0:04  message
//	⠙ label........ 17 items  0:02  message            (indeterminate)
func TaskLine(t *state.Task, phase int, now time.Time, o Options) string {
	w := o.width()
	lw := labelWidth(w)
	var b strings.Builder
	b.WriteString(statusGlyph(t, phase, o))
	b.WriteByte(' ')
	b.WriteString(o.paint(sgrBold, Pad(t.Label, lw, o.ASCII)))
	b.WriteByte(' ')

	if t.Determinate() {
		counts := fmt.Sprintf("%3.0f%% (%d/%d)", t.Percent(), t.Current, t.Total)
		barW := w - lw - len(counts) - 12
		if barW > 30 {
			barW = 30
		}
		if barW >= 4 {
			bar := Bar(t.Current, t.Total, barW, o.ASCII)
			barColor := sgrCyan
			switch t.Status {
			case state.Done:
				barColor = sgrGreen
			case state.Failed:
				barColor = sgrRed
			}
			b.WriteString(o.paint(barColor, bar))
			b.WriteByte(' ')
		}
		b.WriteString(counts)
	} else {
		b.WriteString(fmt.Sprintf("%d %s", t.Current, itemsNoun(t.Current)))
	}
	b.WriteString("  ")
	b.WriteString(o.paint(sgrDim, elapsed(t, now)))

	tail := t.Message
	if t.Status == state.Failed && tail == "" {
		tail = "failed"
	}
	if tail != "" {
		b.WriteString("  ")
		if t.Status == state.Failed {
			b.WriteString(o.paint(sgrRed, tail))
		} else {
			b.WriteString(o.paint(sgrDim, tail))
		}
	}
	// Hard cap the visible length so live repaints never wrap (wrapping
	// corrupts the cursor-up arithmetic in the live loop).
	return truncateVisible(b.String(), w, o.ASCII)
}

// itemsNoun pluralizes the indeterminate-progress unit ("1 item").
func itemsNoun(n int64) string {
	if n == 1 {
		return "item"
	}
	return "items"
}

// SummaryLine renders the dashboard footer.
func SummaryLine(s state.Summary, o Options) string {
	noun := "tasks"
	if s.Tasks == 1 {
		noun = "task"
	}
	parts := []string{fmt.Sprintf("%d %s", s.Tasks, noun)}
	if s.Running > 0 {
		parts = append(parts, o.paint(sgrCyan, fmt.Sprintf("%d running", s.Running)))
	}
	if s.Done > 0 {
		parts = append(parts, o.paint(sgrGreen, fmt.Sprintf("%d done", s.Done)))
	}
	if s.Failed > 0 {
		parts = append(parts, o.paint(sgrRed, fmt.Sprintf("%d failed", s.Failed)))
	}
	if s.TotalSum > 0 {
		parts = append(parts, fmt.Sprintf("overall %.0f%%", s.Percent()))
	}
	if s.Malformed > 0 {
		word := "lines"
		if s.Malformed == 1 {
			word = "line"
		}
		parts = append(parts, o.paint(sgrYellow, fmt.Sprintf("%d malformed %s", s.Malformed, word)))
	}
	return strings.Join(parts, " · ")
}

// Frame renders the whole dashboard: one line per task in creation order,
// a separator-free summary footer, newline-terminated. The live loop
// diffs frame heights itself; Frame stays a pure string builder.
func Frame(d *state.Dashboard, phase int, now time.Time, o Options) string {
	var b strings.Builder
	for _, t := range d.Tasks() {
		b.WriteString(TaskLine(t, phase, now, o))
		b.WriteByte('\n')
	}
	b.WriteString(SummaryLine(d.Summary(), o))
	b.WriteByte('\n')
	return b.String()
}

// visibleLen counts runes in s outside ANSI SGR escape sequences.
func visibleLen(s string) int {
	n := 0
	inEsc := false
	for _, r := range s {
		switch {
		case inEsc:
			if r == 'm' {
				inEsc = false
			}
		case r == 0x1b:
			inEsc = true
		default:
			n++
		}
	}
	return n
}

// truncateVisible cuts s to at most max visible runes (ANSI SGR sequences
// are zero-width), replacing the tail with an ellipsis and closing any
// open color with a reset.
func truncateVisible(s string, max int, ascii bool) string {
	if visibleLen(s) <= max {
		return s
	}
	keep := max - 1 // reserve one cell for the ellipsis
	if keep < 0 {
		keep = 0
	}
	visible := 0
	inEsc := false
	sawColor := false
	cut := len(s)
	for i, r := range s {
		if visible == keep {
			cut = i
			break
		}
		switch {
		case inEsc:
			if r == 'm' {
				inEsc = false
			}
		case r == 0x1b:
			inEsc = true
			sawColor = true
		default:
			visible++
		}
	}
	out := s[:cut]
	if sawColor {
		out += sgrReset
	}
	if ascii {
		return out + "~"
	}
	return out + "…"
}
