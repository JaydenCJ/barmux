// Tests for TTY frame building: task lines, glyphs, colors, the
// no-wrap invariant that keeps in-place repainting stable, and the
// summary footer. All time comes from fixed instants.
package render

import (
	"strings"
	"testing"
	"time"

	"github.com/JaydenCJ/barmux/internal/protocol"
	"github.com/JaydenCJ/barmux/internal/state"
)

var t0 = time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)

// fixedDash builds a dashboard whose clock is pinned to t0.
func fixedDash(t *testing.T, lines ...string) *state.Dashboard {
	t.Helper()
	d := state.New(func() time.Time { return t0 })
	for _, line := range lines {
		ev, err := protocol.ParseLine(line)
		if err != nil {
			t.Fatalf("bad line %q: %v", line, err)
		}
		d.Apply(ev)
	}
	return d
}

func plainOpts(width int) Options { return Options{Width: width, Color: false} }

func TestTaskLineDeterminateLayout(t *testing.T) {
	d := fixedDash(t, "start build 10 Compiling", "tick build 5")
	line := TaskLine(d.Task("build"), 0, t0.Add(4*time.Second), plainOpts(100))
	for _, want := range []string{"Compiling", "50% (5/10)", "█", "░", "0:04"} {
		if !strings.Contains(line, want) {
			t.Fatalf("line %q missing %q", line, want)
		}
	}
}

func TestTaskLineIndeterminateShowsSpinnerAndCount(t *testing.T) {
	d := fixedDash(t, "start pull - Downloading", "tick pull 17")
	line := TaskLine(d.Task("pull"), 3, t0, plainOpts(100))
	if !strings.Contains(line, Spinner(3, false)) {
		t.Fatalf("spinner frame missing: %q", line)
	}
	if !strings.Contains(line, "17 items") {
		t.Fatalf("item count missing: %q", line)
	}
	if strings.Contains(line, "%") {
		t.Fatalf("indeterminate line must not show a percent: %q", line)
	}
}

func TestTaskLineSingularItemCount(t *testing.T) {
	d := fixedDash(t, "start pull -", "tick pull")
	line := TaskLine(d.Task("pull"), 0, t0, plainOpts(100))
	if !strings.Contains(line, "1 item") || strings.Contains(line, "1 items") {
		t.Fatalf("noun must agree with the count: %q", line)
	}
}

func TestTaskLineStatusGlyphsAndFailureTail(t *testing.T) {
	d := fixedDash(t, "start a 2", "done a", "start b 2", "fail b broke", "start c 2", "fail c")
	doneLine := TaskLine(d.Task("a"), 0, t0, plainOpts(100))
	failLine := TaskLine(d.Task("b"), 0, t0, plainOpts(100))
	if !strings.HasPrefix(doneLine, "✔") {
		t.Fatalf("done glyph: %q", doneLine)
	}
	if !strings.HasPrefix(failLine, "✘") || !strings.Contains(failLine, "broke") {
		t.Fatalf("fail glyph/reason: %q", failLine)
	}
	// A fail without a reason still explains itself.
	if line := TaskLine(d.Task("c"), 0, t0, plainOpts(100)); !strings.Contains(line, "failed") {
		t.Fatalf("default failure tail missing: %q", line)
	}
}

func TestTaskLineASCIIUsesNoUnicode(t *testing.T) {
	d := fixedDash(t, "start a 4 Build", "tick a 2", "start b -", "start c 1", "done c", "start e 1", "fail e")
	opts := Options{Width: 100, ASCII: true}
	var all strings.Builder
	for _, task := range d.Tasks() {
		all.WriteString(TaskLine(task, 1, t0, opts))
		all.WriteByte('\n')
	}
	for _, r := range all.String() {
		if r > 127 {
			t.Fatalf("non-ASCII rune %q in ASCII mode output:\n%s", r, all.String())
		}
	}
}

func TestTaskLineColorToggle(t *testing.T) {
	d := fixedDash(t, "start a 2", "done a")
	line := TaskLine(d.Task("a"), 0, t0, Options{Width: 100, Color: true})
	if !strings.Contains(line, "\x1b[32m") || !strings.Contains(line, "\x1b[0m") {
		t.Fatalf("green SGR + reset expected: %q", line)
	}
	if line := TaskLine(d.Task("a"), 0, t0, plainOpts(100)); strings.Contains(line, "\x1b") {
		t.Fatalf("escape leaked without color: %q", line)
	}
}

func TestTaskLineNeverExceedsWidth(t *testing.T) {
	// Wrapping a live line breaks the cursor-up arithmetic, so this is
	// the safety invariant of the whole live renderer.
	d := fixedDash(t,
		"start verylongtaskname 100 An extremely long human readable label for the task",
		"tick verylongtaskname 37",
		"msg verylongtaskname now processing a very long file name deep in the tree",
	)
	for _, width := range []int{20, 40, 60, 80, 120} {
		for _, color := range []bool{false, true} {
			line := TaskLine(d.Task("verylongtaskname"), 0, t0, Options{Width: width, Color: color})
			if got := visibleLen(line); got > width {
				t.Fatalf("width %d color %v: line is %d cells: %q", width, color, got, line)
			}
		}
	}
}

func TestElapsedFormatting(t *testing.T) {
	d := fixedDash(t, "start a 1")
	task := d.Task("a")
	cases := []struct {
		after time.Duration
		want  string
	}{
		{0, "0:00"},
		{59 * time.Second, "0:59"},
		{61 * time.Second, "1:01"},
		{3661 * time.Second, "1:01:01"},
	}
	for _, c := range cases {
		line := TaskLine(task, 0, t0.Add(c.after), plainOpts(120))
		if !strings.Contains(line, c.want) {
			t.Fatalf("after %v want %q in %q", c.after, c.want, line)
		}
	}
}

func TestElapsedFreezesAtFinish(t *testing.T) {
	// Once a task is done its clock must stop, no matter how much later
	// the frame is painted.
	clock := t0
	d := state.New(func() time.Time { return clock })
	ev, _ := protocol.ParseLine("start a 1")
	d.Apply(ev)
	clock = t0.Add(5 * time.Second)
	ev, _ = protocol.ParseLine("done a")
	d.Apply(ev)
	line := TaskLine(d.Task("a"), 0, t0.Add(2*time.Hour), plainOpts(100))
	if !strings.Contains(line, "0:05") {
		t.Fatalf("elapsed should freeze at 0:05: %q", line)
	}
}

func TestSummaryLineVariants(t *testing.T) {
	s := state.Summary{Tasks: 3, Running: 1, Done: 1, Failed: 1, CurrentSum: 15, TotalSum: 20}
	got := SummaryLine(s, Options{})
	for _, want := range []string{"3 tasks", "1 running", "1 done", "1 failed", "overall 75%"} {
		if !strings.Contains(got, want) {
			t.Fatalf("summary %q missing %q", got, want)
		}
	}
	// Singular noun, and zero-valued parts are omitted entirely.
	got = SummaryLine(state.Summary{Tasks: 1, Done: 1}, Options{})
	if !strings.HasPrefix(got, "1 task ") {
		t.Fatalf("singular noun: %q", got)
	}
	for _, absent := range []string{"running", "failed", "overall", "malformed"} {
		if strings.Contains(got, absent) {
			t.Fatalf("summary %q should omit %q", got, absent)
		}
	}
	// Malformed input is surfaced, never hidden.
	got = SummaryLine(state.Summary{Tasks: 1, Malformed: 4}, Options{})
	if !strings.Contains(got, "4 malformed lines") {
		t.Fatalf("malformed count missing: %q", got)
	}
}

func TestFrameLayoutAndOrder(t *testing.T) {
	d := fixedDash(t, "start z 2 Zulu", "start a 1 Alpha", "tick z")
	frame := Frame(d, 0, t0, plainOpts(80))
	lines := strings.Split(strings.TrimRight(frame, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("want 2 task lines + summary, got %d:\n%s", len(lines), frame)
	}
	if !strings.Contains(lines[2], "2 tasks") {
		t.Fatalf("summary must be last: %q", lines[2])
	}
	if strings.Index(frame, "Zulu") > strings.Index(frame, "Alpha") {
		t.Fatalf("creation order changed:\n%s", frame)
	}
}

func TestOptionsWidthClamping(t *testing.T) {
	if got := (Options{}).width(); got != DefaultWidth {
		t.Fatalf("default width: %d", got)
	}
	if got := (Options{Width: 5}).width(); got != 20 {
		t.Fatalf("tiny widths clamp to 20, got %d", got)
	}
}

func TestTruncateVisibleIgnoresSGR(t *testing.T) {
	if got := visibleLen("\x1b[1mab\x1b[0mc"); got != 3 {
		t.Fatalf("visibleLen: %d", got)
	}
	in := "\x1b[32m" + strings.Repeat("a", 30) + "\x1b[0m"
	out := truncateVisible(in, 10, false)
	if got := visibleLen(out); got != 10 {
		t.Fatalf("visible length: %d (%q)", got, out)
	}
	if !strings.HasSuffix(out, "…") {
		t.Fatalf("ellipsis missing: %q", out)
	}
	if !strings.Contains(out, "\x1b[0m") {
		t.Fatalf("cut color must be reset: %q", out)
	}
}
