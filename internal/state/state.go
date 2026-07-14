// Package state holds the dashboard model: the set of tasks announced over
// the pipe, their progress, and aggregate summaries. It is pure — no I/O,
// no goroutines — and takes time through an injected clock so every
// transition is unit-testable and deterministic.
package state

import (
	"time"

	"github.com/JaydenCJ/barmux/internal/protocol"
)

// Status is the lifecycle state of a task.
type Status int

const (
	// Running covers everything between the first event and done/fail.
	Running Status = iota
	// Done means the task finished successfully (`done` verb).
	Done
	// Failed means the task reported failure (`fail` verb).
	Failed
)

// String returns a lowercase status name for rendering.
func (s Status) String() string {
	switch s {
	case Running:
		return "running"
	case Done:
		return "done"
	case Failed:
		return "failed"
	}
	return "unknown"
}

// Task is one progress bar's worth of state.
type Task struct {
	ID      string
	Label   string // display name; defaults to ID
	Total   int64  // 0 means indeterminate (spinner, not a bar)
	Current int64
	Status  Status
	Message string // last msg text, or fail reason
	Seq     int    // creation order, stable across the run

	StartedAt  time.Time
	FinishedAt time.Time
}

// Determinate reports whether the task has a known total.
func (t *Task) Determinate() bool { return t.Total > 0 }

// Percent returns completion in [0,100] for determinate tasks, 0 otherwise.
func (t *Task) Percent() float64 {
	if t.Total <= 0 {
		return 0
	}
	p := float64(t.Current) / float64(t.Total) * 100
	if p > 100 {
		p = 100
	}
	return p
}

// Summary aggregates the whole dashboard for the footer line and for
// exit-code decisions.
type Summary struct {
	Tasks     int
	Running   int
	Done      int
	Failed    int
	Malformed int // malformed protocol lines seen

	// CurrentSum/TotalSum aggregate determinate tasks only, for an
	// overall percentage that is meaningful when totals are comparable
	// units (files, tests, packages).
	CurrentSum int64
	TotalSum   int64
}

// Percent returns the overall completion of all determinate work.
func (s Summary) Percent() float64 {
	if s.TotalSum <= 0 {
		return 0
	}
	p := float64(s.CurrentSum) / float64(s.TotalSum) * 100
	if p > 100 {
		p = 100
	}
	return p
}

// Dashboard applies protocol events to a task table.
type Dashboard struct {
	now       func() time.Time
	tasks     map[string]*Task
	order     []string
	logs      []string // pending pass-through log lines, drained by renderers
	malformed int
}

// New returns an empty dashboard reading time from now (may not be nil).
func New(now func() time.Time) *Dashboard {
	return &Dashboard{now: now, tasks: make(map[string]*Task)}
}

// upsert returns the task for id, creating it implicitly if needed.
// Implicit creation is a deliberate protocol feature: `tick build` with no
// prior `start` still renders, so partial instrumentation degrades softly.
func (d *Dashboard) upsert(id string) *Task {
	if t, ok := d.tasks[id]; ok {
		return t
	}
	t := &Task{
		ID:        id,
		Label:     id,
		Status:    Running,
		Seq:       len(d.order),
		StartedAt: d.now(),
	}
	d.tasks[id] = t
	d.order = append(d.order, id)
	return t
}

// Apply folds one event into the dashboard. Unknown-verb and malformed
// lines never reach Apply; call CountMalformed for those.
func (d *Dashboard) Apply(e protocol.Event) {
	switch e.Kind {
	case protocol.KindLog:
		d.logs = append(d.logs, e.Text)
		return
	case protocol.KindStart:
		t := d.upsert(e.ID)
		if e.HasN {
			t.Total = e.N
		}
		if e.Text != "" {
			t.Label = e.Text
		}
		// Re-starting a finished task reopens it (a retried build step).
		if t.Status != Running {
			t.Status = Running
			t.Current = 0
			t.Message = ""
			t.StartedAt = d.now()
			t.FinishedAt = time.Time{}
		}
		t.clamp() // a re-announced, smaller total reclamps like `total` does
	case protocol.KindTick:
		t := d.upsert(e.ID)
		t.Current += e.N
		t.clamp()
	case protocol.KindSet:
		t := d.upsert(e.ID)
		t.Current = e.N
		t.clamp()
	case protocol.KindTotal:
		t := d.upsert(e.ID)
		t.Total = e.N
		t.clamp()
	case protocol.KindMsg:
		t := d.upsert(e.ID)
		t.Message = e.Text
	case protocol.KindDone:
		t := d.upsert(e.ID)
		t.Status = Done
		t.FinishedAt = d.now()
		if e.Text != "" {
			t.Message = e.Text
		}
		if t.Total > 0 {
			t.Current = t.Total // done implies complete
		}
	case protocol.KindFail:
		t := d.upsert(e.ID)
		t.Status = Failed
		t.FinishedAt = d.now()
		t.Message = e.Text
	}
}

// clamp keeps Current within [0, Total] for determinate tasks.
func (t *Task) clamp() {
	if t.Current < 0 {
		t.Current = 0
	}
	if t.Total > 0 && t.Current > t.Total {
		t.Current = t.Total
	}
}

// CountMalformed records one malformed input line for the summary.
func (d *Dashboard) CountMalformed() { d.malformed++ }

// Task returns the task with the given id, or nil.
func (d *Dashboard) Task(id string) *Task { return d.tasks[id] }

// Tasks returns all tasks in creation order. The returned slice is fresh;
// the *Task pointers are live.
func (d *Dashboard) Tasks() []*Task {
	out := make([]*Task, 0, len(d.order))
	for _, id := range d.order {
		out = append(out, d.tasks[id])
	}
	return out
}

// DrainLogs returns and clears the pending pass-through log lines.
func (d *Dashboard) DrainLogs() []string {
	out := d.logs
	d.logs = nil
	return out
}

// Summary computes aggregate counts over the current task table.
func (d *Dashboard) Summary() Summary {
	s := Summary{Malformed: d.malformed}
	for _, id := range d.order {
		t := d.tasks[id]
		s.Tasks++
		switch t.Status {
		case Running:
			s.Running++
		case Done:
			s.Done++
		case Failed:
			s.Failed++
		}
		if t.Total > 0 {
			s.TotalSum += t.Total
			s.CurrentSum += t.Current
		}
	}
	return s
}
