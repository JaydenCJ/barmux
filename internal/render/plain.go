// Plain rendering: the CI fallback. When output is not a TTY (a CI log, a
// file, a pipe) live repainting would spew escape codes, so barmux instead
// streams one append-only milestone line per meaningful transition:
// task start, every N percent of progress, message changes, done/fail,
// and pass-through logs. The result reads like a well-behaved build log.
package render

import (
	"fmt"
	"io"

	"github.com/JaydenCJ/barmux/internal/protocol"
	"github.com/JaydenCJ/barmux/internal/state"
)

// Plain is a stateful streaming renderer. Feed it every event with Handle
// (after applying the event to the dashboard) and finish with Summary.
type Plain struct {
	w    io.Writer
	d    *state.Dashboard
	o    Options
	last map[string]int64 // last milestone bucket announced per task
}

// NewPlain returns a plain renderer writing to w over dashboard d.
func NewPlain(w io.Writer, d *state.Dashboard, o Options) *Plain {
	if o.Step <= 0 {
		o.Step = 10
	}
	if o.Step > 100 {
		o.Step = 100
	}
	return &Plain{w: w, d: d, o: o, last: make(map[string]int64)}
}

// tag renders the [id] prefix of every plain line.
func (p *Plain) tag(id string) string {
	return fmt.Sprintf("[%s]", id)
}

// Handle emits milestone lines for one event that has already been applied
// to the dashboard.
func (p *Plain) Handle(e protocol.Event) {
	switch e.Kind {
	case protocol.KindLog:
		for _, line := range p.d.DrainLogs() {
			fmt.Fprintln(p.w, line)
		}
	case protocol.KindStart:
		t := p.d.Task(e.ID)
		if t == nil {
			return
		}
		p.last[e.ID] = 0
		if t.Determinate() {
			fmt.Fprintf(p.w, "%s start: %s (0/%d)\n", p.tag(e.ID), t.Label, t.Total)
		} else {
			fmt.Fprintf(p.w, "%s start: %s\n", p.tag(e.ID), t.Label)
		}
	case protocol.KindTick, protocol.KindSet, protocol.KindTotal:
		p.progress(e.ID)
	case protocol.KindMsg:
		// Messages are ephemeral status, not log lines: they ride on the
		// next milestone (and on fail) instead of spamming CI output.
		// Children that want a durable line in the log use `log`.
		return
	case protocol.KindDone:
		t := p.d.Task(e.ID)
		if t == nil {
			return
		}
		if t.Determinate() {
			fmt.Fprintf(p.w, "%s done (%d/%d)\n", p.tag(e.ID), t.Current, t.Total)
		} else {
			fmt.Fprintf(p.w, "%s done (%d %s)\n", p.tag(e.ID), t.Current, itemsNoun(t.Current))
		}
	case protocol.KindFail:
		t := p.d.Task(e.ID)
		if t == nil {
			return
		}
		reason := t.Message
		if reason == "" {
			reason = "failed"
		}
		fmt.Fprintf(p.w, "%s FAIL: %s\n", p.tag(e.ID), reason)
	}
}

// progress prints a milestone when a task crosses into a new Step-percent
// bucket. Implicitly-created tasks (tick before start) get a start line
// first, so logs always introduce every task.
func (p *Plain) progress(id string) {
	t := p.d.Task(id)
	if t == nil {
		return
	}
	if _, seen := p.last[id]; !seen {
		p.last[id] = 0
		if t.Determinate() {
			fmt.Fprintf(p.w, "%s start: %s (0/%d)\n", p.tag(id), t.Label, t.Total)
		} else {
			fmt.Fprintf(p.w, "%s start: %s\n", p.tag(id), t.Label)
		}
	}
	if !t.Determinate() {
		return // indeterminate ticks are noise in a log; done reports the count
	}
	bucket := int64(t.Percent()) / int64(p.o.Step)
	if bucket <= p.last[id] {
		return
	}
	p.last[id] = bucket
	line := fmt.Sprintf("%s %3.0f%% (%d/%d)", p.tag(id), t.Percent(), t.Current, t.Total)
	if t.Message != "" {
		line += "  " + t.Message
	}
	fmt.Fprintln(p.w, line)
}

// Summary prints the final aggregate line, matching the TTY footer.
func (p *Plain) Summary() {
	fmt.Fprintln(p.w, SummaryLine(p.d.Summary(), Options{Color: false}))
}
