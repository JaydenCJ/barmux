// Tests for the dashboard state machine: task lifecycle, implicit
// creation, clamping, aggregate summaries — all against a fake clock so
// elapsed-time bookkeeping is fully deterministic.
package state

import (
	"testing"
	"time"

	"github.com/JaydenCJ/barmux/internal/protocol"
)

// fakeClock advances by one second per call, starting at a fixed epoch.
type fakeClock struct {
	t time.Time
}

func newClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) now() time.Time {
	c.t = c.t.Add(time.Second)
	return c.t
}

func newDash() *Dashboard { return New(newClock().now) }

func apply(t *testing.T, d *Dashboard, lines ...string) {
	t.Helper()
	for _, line := range lines {
		ev, err := protocol.ParseLine(line)
		if err != nil {
			t.Fatalf("bad test line %q: %v", line, err)
		}
		d.Apply(ev)
	}
}

func TestStartCreatesTaskWithLabelAndTotal(t *testing.T) {
	d := newDash()
	apply(t, d, "start build 100 Compiling objects")
	task := d.Task("build")
	if task == nil {
		t.Fatal("task not created")
	}
	if task.Label != "Compiling objects" || task.Total != 100 || task.Status != Running {
		t.Fatalf("wrong task: %+v", task)
	}
	// Without a label, the id doubles as the display name.
	apply(t, d, "start deploy 10")
	if got := d.Task("deploy").Label; got != "deploy" {
		t.Fatalf("label should default to id, got %q", got)
	}
}

func TestTickBeforeStartCreatesTaskImplicitly(t *testing.T) {
	// Partial instrumentation must degrade softly: a bare `tick` renders
	// as an indeterminate counter instead of being dropped.
	d := newDash()
	apply(t, d, "tick fetch", "tick fetch")
	task := d.Task("fetch")
	if task == nil {
		t.Fatal("implicit task not created")
	}
	if task.Current != 2 || task.Determinate() {
		t.Fatalf("implicit task wrong: %+v", task)
	}
}

func TestTickAccumulatesAndClampsAtTotal(t *testing.T) {
	d := newDash()
	apply(t, d, "start b 5", "tick b 3", "tick b 3")
	if got := d.Task("b").Current; got != 5 {
		t.Fatalf("tick must clamp at total: got %d", got)
	}
}

func TestSetOverridesAndClamps(t *testing.T) {
	d := newDash()
	apply(t, d, "start b 10", "tick b 9", "set b 2")
	if got := d.Task("b").Current; got != 2 {
		t.Fatalf("set should override, got %d", got)
	}
	apply(t, d, "set b 999")
	if got := d.Task("b").Current; got != 10 {
		t.Fatalf("set must clamp at total: got %d", got)
	}
}

func TestTotalCanArriveLateAndReclamps(t *testing.T) {
	// A discovery phase often learns the item count only after work
	// started; `total` retro-fits the bar and clamps progress.
	d := newDash()
	apply(t, d, "tick scan 30", "total scan 20")
	task := d.Task("scan")
	if task.Total != 20 || task.Current != 20 {
		t.Fatalf("late total wrong: %+v", task)
	}
}

func TestMsgUpdatesMessageOnly(t *testing.T) {
	d := newDash()
	apply(t, d, "start b 10", "tick b", "msg b compiling main.c")
	task := d.Task("b")
	if task.Message != "compiling main.c" || task.Current != 1 {
		t.Fatalf("msg side effects: %+v", task)
	}
}

func TestDoneCompletesDeterminateTask(t *testing.T) {
	d := newDash()
	apply(t, d, "start b 10", "tick b 4", "done b all green")
	task := d.Task("b")
	if task.Status != Done {
		t.Fatalf("status: %v", task.Status)
	}
	if task.Current != 10 {
		t.Fatalf("done implies complete; current = %d", task.Current)
	}
	if task.Message != "all green" {
		t.Fatalf("done text lost: %q", task.Message)
	}
	if task.FinishedAt.IsZero() {
		t.Fatal("FinishedAt not stamped")
	}
}

func TestDoneKeepsCountForIndeterminateTask(t *testing.T) {
	d := newDash()
	apply(t, d, "start pull - Downloading", "tick pull 17", "done pull")
	task := d.Task("pull")
	if task.Current != 17 {
		t.Fatalf("indeterminate done must keep the item count, got %d", task.Current)
	}
}

func TestFailRecordsReason(t *testing.T) {
	d := newDash()
	apply(t, d, "start test 50", "tick test 12", "fail test segfault in worker")
	task := d.Task("test")
	if task.Status != Failed || task.Message != "segfault in worker" {
		t.Fatalf("fail wrong: %+v", task)
	}
	if task.Current != 12 {
		t.Fatalf("fail must not touch progress, got %d", task.Current)
	}
}

func TestRestartSemantics(t *testing.T) {
	// A retried build step restarts its bar from zero...
	d := newDash()
	apply(t, d, "start b 10", "tick b 10", "done b", "start b 10 second attempt")
	task := d.Task("b")
	if task.Status != Running || task.Current != 0 {
		t.Fatalf("restart should reopen: %+v", task)
	}
	if task.Label != "second attempt" {
		t.Fatalf("restart label: %q", task.Label)
	}
	if !task.FinishedAt.IsZero() {
		t.Fatal("FinishedAt should reset on restart")
	}
	// ...but re-announcing a RUNNING task must not reset progress (two
	// workers may both send `start` for a shared bar).
	apply(t, d, "tick b 4", "start b 20")
	if task.Current != 4 || task.Total != 20 {
		t.Fatalf("running re-start should keep progress: %+v", task)
	}
	// A re-announced, smaller total reclamps progress, like `total` does.
	apply(t, d, "start b 3")
	if task.Total != 3 || task.Current != 3 {
		t.Fatalf("shrunk re-start should reclamp: %+v", task)
	}
}

func TestTasksKeepCreationOrderAndClock(t *testing.T) {
	d := newDash()
	apply(t, d, "start c 1", "start a 1", "tick b")
	var ids []string
	for _, task := range d.Tasks() {
		ids = append(ids, task.ID)
	}
	if len(ids) != 3 || ids[0] != "c" || ids[1] != "a" || ids[2] != "b" {
		t.Fatalf("order not stable: %v", ids)
	}
	if d.Task("a").Seq != 1 {
		t.Fatalf("seq wrong: %d", d.Task("a").Seq)
	}
	// The injected clock stamps every creation, so later tasks start later.
	if !d.Task("a").StartedAt.After(d.Task("c").StartedAt) {
		t.Fatal("clock not consulted per task")
	}
}

func TestLogsAreBufferedAndDrained(t *testing.T) {
	d := newDash()
	apply(t, d, "log first", "log second")
	logs := d.DrainLogs()
	if len(logs) != 2 || logs[0] != "first" || logs[1] != "second" {
		t.Fatalf("logs: %v", logs)
	}
	if len(d.DrainLogs()) != 0 {
		t.Fatal("drain must clear the buffer")
	}
}

func TestPercentMath(t *testing.T) {
	d := newDash()
	apply(t, d, "start b 3", "tick b")
	if got := d.Task("b").Percent(); got < 33.2 || got > 33.4 {
		t.Fatalf("percent: %f", got)
	}
	apply(t, d, "start i -", "tick i 100")
	if got := d.Task("i").Percent(); got != 0 {
		t.Fatalf("indeterminate percent should be 0, got %f", got)
	}
	// A dashboard with no determinate work has no meaningful overall
	// percentage; it must report 0, not NaN or a divide-by-zero panic.
	d2 := newDash()
	apply(t, d2, "start a -", "tick a 100")
	if got := d2.Summary().Percent(); got != 0 {
		t.Fatalf("no determinate work: %f", got)
	}
}

func TestSummaryAggregates(t *testing.T) {
	d := newDash()
	apply(t, d,
		"start a 10", "tick a 5",
		"start b 10", "tick b 10", "done b",
		"start c -", "tick c 3",
		"fail c oom",
	)
	d.CountMalformed()
	s := d.Summary()
	if s.Tasks != 3 || s.Running != 1 || s.Done != 1 || s.Failed != 1 {
		t.Fatalf("counts: %+v", s)
	}
	if s.CurrentSum != 15 || s.TotalSum != 20 {
		t.Fatalf("sums must cover determinate tasks only: %+v", s)
	}
	if got := s.Percent(); got != 75 {
		t.Fatalf("overall percent: %f", got)
	}
	if s.Malformed != 1 {
		t.Fatalf("malformed: %d", s.Malformed)
	}
}

func TestUnknownTaskLookupAndStatusNames(t *testing.T) {
	d := newDash()
	if d.Task("ghost") != nil {
		t.Fatal("unknown id must return nil")
	}
	if len(d.Tasks()) != 0 {
		t.Fatal("fresh dashboard must be empty")
	}
	if Running.String() != "running" || Done.String() != "done" || Failed.String() != "failed" {
		t.Fatal("status names are rendered; keep them stable")
	}
	if Status(9).String() != "unknown" {
		t.Fatal("unknown status fallback")
	}
}
