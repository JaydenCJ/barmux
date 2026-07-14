// Tests for the plain (CI fallback) renderer: the append-only milestone
// log that non-TTY consumers see. Output is asserted line-exactly — this
// format is what people grep in CI, so it is a compatibility surface.
package render

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/JaydenCJ/barmux/internal/protocol"
	"github.com/JaydenCJ/barmux/internal/state"
)

// feed parses each line, applies it to a fresh dashboard, streams it
// through Plain, and returns everything written.
func feed(t *testing.T, opts Options, lines ...string) string {
	t.Helper()
	var out strings.Builder
	d := state.New(func() time.Time { return t0 })
	p := NewPlain(&out, d, opts)
	for _, line := range lines {
		ev, err := protocol.ParseLine(line)
		if err != nil {
			t.Fatalf("bad line %q: %v", line, err)
		}
		d.Apply(ev)
		p.Handle(ev)
	}
	return out.String()
}

func TestPlainStartLines(t *testing.T) {
	out := feed(t, Options{}, "start build 10 Compiling objects")
	if out != "[build] start: Compiling objects (0/10)\n" {
		t.Fatalf("determinate start line: %q", out)
	}
	out = feed(t, Options{}, "start pull - Downloading layers")
	if out != "[pull] start: Downloading layers\n" {
		t.Fatalf("indeterminate start line: %q", out)
	}
}

func TestPlainMilestoneBucketsThrottleChattyChildren(t *testing.T) {
	ticks := func(n int) []string {
		lines := []string{"start b " + strconv.Itoa(n)}
		for i := 0; i < n; i++ {
			lines = append(lines, "tick b")
		}
		return lines
	}
	// 10 ticks on a 10-step task: every tick is a new 10% bucket.
	out := feed(t, Options{}, ticks(10)...)
	if got := strings.Count(out, "%"); got != 10 {
		t.Fatalf("want 10 milestones, got %d:\n%s", got, out)
	}
	if !strings.Contains(out, "[b]  10% (1/10)") || !strings.Contains(out, "[b] 100% (10/10)") {
		t.Fatalf("milestone content:\n%s", out)
	}
	// 100 ticks but still only 10 bucket crossings: CI logs stay readable
	// no matter how chatty the child is.
	out = feed(t, Options{}, ticks(100)...)
	if got := strings.Count(out, "%"); got != 10 {
		t.Fatalf("want 10 milestones from 100 ticks, got %d", got)
	}
}

func TestPlainStepOptionWidensBuckets(t *testing.T) {
	lines := []string{"start b 100"}
	for i := 0; i < 100; i++ {
		lines = append(lines, "tick b")
	}
	out := feed(t, Options{Step: 25}, lines...)
	if got := strings.Count(out, "%"); got != 4 {
		t.Fatalf("step 25 should yield 4 milestones, got %d:\n%s", got, out)
	}
	// Step 0 and negative fall back to 10; absurd steps clamp to 100.
	d := state.New(func() time.Time { return t0 })
	if p := NewPlain(&strings.Builder{}, d, Options{Step: -5}); p.o.Step != 10 {
		t.Fatalf("negative step: %d", p.o.Step)
	}
	if p := NewPlain(&strings.Builder{}, d, Options{Step: 500}); p.o.Step != 100 {
		t.Fatalf("huge step: %d", p.o.Step)
	}
}

func TestPlainBigJumpPrintsOnlyLatestBucket(t *testing.T) {
	out := feed(t, Options{}, "start b 100", "set b 87")
	if got := strings.Count(out, "%"); got != 1 {
		t.Fatalf("a jump prints one milestone, got %d:\n%s", got, out)
	}
	if !strings.Contains(out, "87% (87/100)") {
		t.Fatalf("actual progress shown:\n%s", out)
	}
}

func TestPlainImplicitTaskGetsIntroduced(t *testing.T) {
	// tick-before-start must still introduce the task so the log
	// explains every [id] tag it uses.
	out := feed(t, Options{}, "tick fetch")
	if !strings.Contains(out, "[fetch] start: fetch") {
		t.Fatalf("implicit intro missing:\n%s", out)
	}
}

func TestPlainIndeterminateTicksAreSilent(t *testing.T) {
	out := feed(t, Options{}, "start pull - Layers", "tick pull", "tick pull", "tick pull")
	if got := strings.Count(out, "\n"); got != 1 {
		t.Fatalf("indeterminate ticks must not spam the log:\n%s", out)
	}
}

func TestPlainMessagesRideOnMilestonesOnly(t *testing.T) {
	// msg is ephemeral status: a standalone msg prints nothing (that is
	// what `log` is for), but the latest message decorates milestones.
	out := feed(t, Options{}, "start b 10", "msg b cc main.c")
	if strings.Contains(out, "cc main.c") {
		t.Fatalf("bare msg must not print in CI mode:\n%s", out)
	}
	out = feed(t, Options{}, "start b 10", "msg b linking", "tick b 5")
	if !strings.Contains(out, "50% (5/10)  linking") {
		t.Fatalf("message not attached to milestone:\n%s", out)
	}
}

func TestPlainDoneLines(t *testing.T) {
	out := feed(t, Options{}, "start b 10", "tick b 10", "done b")
	if !strings.Contains(out, "[b] done (10/10)\n") {
		t.Fatalf("done line:\n%s", out)
	}
	out = feed(t, Options{}, "start p -", "tick p 17", "done p")
	if !strings.Contains(out, "[p] done (17 items)\n") {
		t.Fatalf("indeterminate done line:\n%s", out)
	}
}

func TestPlainSingularItemCount(t *testing.T) {
	// "1 items" is the kind of paper cut people screenshot; the noun
	// agrees with the count.
	out := feed(t, Options{}, "start p -", "tick p", "done p")
	if !strings.Contains(out, "[p] done (1 item)\n") {
		t.Fatalf("singular done line:\n%s", out)
	}
}

func TestPlainFailLineWithAndWithoutReason(t *testing.T) {
	out := feed(t, Options{}, "start b 10", "fail b out of memory")
	if !strings.Contains(out, "[b] FAIL: out of memory\n") {
		t.Fatalf("fail line:\n%s", out)
	}
	out = feed(t, Options{}, "start b 10", "fail b")
	if !strings.Contains(out, "[b] FAIL: failed\n") {
		t.Fatalf("default fail reason:\n%s", out)
	}
}

func TestPlainLogPassthrough(t *testing.T) {
	out := feed(t, Options{}, "log a raw line from a child")
	if out != "a raw line from a child\n" {
		t.Fatalf("log passthrough: %q", out)
	}
}

func TestPlainSummaryMatchesFooter(t *testing.T) {
	var out strings.Builder
	d := state.New(func() time.Time { return t0 })
	p := NewPlain(&out, d, Options{})
	for _, line := range []string{"start a 2", "tick a 2", "done a"} {
		ev, _ := protocol.ParseLine(line)
		d.Apply(ev)
		p.Handle(ev)
	}
	p.Summary()
	if !strings.Contains(out.String(), "1 task · 1 done · overall 100%") {
		t.Fatalf("summary:\n%s", out.String())
	}
}
