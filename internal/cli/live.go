// The live TTY render loop: repaint the dashboard region in place at a
// bounded frame rate, printing log lines *above* the region so scrollback
// stays a clean, complete log while the bars keep animating below it.
package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/JaydenCJ/barmux/internal/render"
	"github.com/JaydenCJ/barmux/internal/state"
)

// ansiUp moves the cursor up n lines; ansiClearLine erases the current
// line. Together they let us redraw the frame region without flicker.
func ansiUp(n int) string { return fmt.Sprintf("\x1b[%dA", n) }

const ansiClearLine = "\x1b[2K\r"

// liveLoop consumes events and repaints at most fps times per second.
// Repaints are driven by a ticker rather than by event arrival, so a
// child spamming ticks cannot melt the terminal.
func (a *App) liveLoop(d *state.Dashboard, events <-chan item, opts render.Options, fps int) {
	ticker := time.NewTicker(time.Second / time.Duration(fps))
	defer ticker.Stop()

	painted := 0 // lines currently occupied by the frame region
	phase := 0
	dirty := true

	erase := func() {
		if painted == 0 {
			return
		}
		var b strings.Builder
		b.WriteString(ansiUp(painted))
		for i := 0; i < painted; i++ {
			b.WriteString(ansiClearLine)
			if i < painted-1 {
				b.WriteString("\n")
			}
		}
		// ansiUp(0) would still move up one line (CSI treats a 0 count
		// as 1), so a single-line region needs no re-positioning at all.
		if painted > 1 {
			b.WriteString(ansiUp(painted - 1))
		}
		fmt.Fprint(a.Stdout, b.String())
		painted = 0
	}

	paint := func() {
		erase()
		for _, line := range d.DrainLogs() {
			fmt.Fprintln(a.Stdout, line)
		}
		frame := render.Frame(d, phase, a.now()(), opts)
		fmt.Fprint(a.Stdout, frame)
		painted = strings.Count(frame, "\n")
		dirty = false
	}

	for {
		select {
		case it, ok := <-events:
			if !ok {
				phase++
				paint() // final frame: real completion state, drained logs
				return
			}
			if it.bad {
				d.CountMalformed()
			} else {
				d.Apply(it.ev)
			}
			dirty = true
		case <-ticker.C:
			phase++
			if dirty || hasRunning(d) {
				paint() // spinner/elapsed animate even between events
			}
		}
	}
}

// hasRunning reports whether any task is still animating.
func hasRunning(d *state.Dashboard) bool {
	for _, t := range d.Tasks() {
		if t.Status == state.Running {
			return true
		}
	}
	return false
}
