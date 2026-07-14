// Tests for the wire protocol: parsing every verb, the failure modes a
// dumb text protocol must survive (torn lines, typos, hostile input), and
// the ParseLine/FormatEvent round-trip that keeps `barmux emit` and raw
// `echo` byte-identical on the wire.
package protocol

import (
	"errors"
	"strings"
	"testing"
)

func mustParse(t *testing.T, line string) Event {
	t.Helper()
	ev, err := ParseLine(line)
	if err != nil {
		t.Fatalf("ParseLine(%q): unexpected error: %v", line, err)
	}
	return ev
}

func TestParseStartWithTotalAndLabel(t *testing.T) {
	ev := mustParse(t, "start build 100 Compiling objects")
	if ev.Kind != KindStart || ev.ID != "build" {
		t.Fatalf("wrong kind/id: %+v", ev)
	}
	if !ev.HasN || ev.N != 100 {
		t.Fatalf("total not parsed: %+v", ev)
	}
	if ev.Text != "Compiling objects" {
		t.Fatalf("label should keep spaces, got %q", ev.Text)
	}
	// total=0 is legal on the wire (an empty work list); state treats it
	// as indeterminate for rendering, but parsing must not reject it.
	ev = mustParse(t, "start noop 0")
	if !ev.HasN || ev.N != 0 {
		t.Fatalf("zero total should parse: %+v", ev)
	}
}

func TestParseStartIndeterminateForms(t *testing.T) {
	// Bare start: no total, no label.
	ev := mustParse(t, "start pull")
	if ev.HasN || ev.Text != "" {
		t.Fatalf("bare start must be indeterminate and unlabeled: %+v", ev)
	}
	// "-" is the explicit indeterminate spelling so a label can follow.
	ev = mustParse(t, "start pull - Downloading layers")
	if ev.HasN {
		t.Fatalf("dash total must be indeterminate: %+v", ev)
	}
	if ev.Text != "Downloading layers" {
		t.Fatalf("label lost: %q", ev.Text)
	}
}

func TestParseTickCounts(t *testing.T) {
	ev := mustParse(t, "tick build")
	if ev.Kind != KindTick || !ev.HasN || ev.N != 1 {
		t.Fatalf("tick default should be 1: %+v", ev)
	}
	if ev = mustParse(t, "tick build 25"); ev.N != 25 {
		t.Fatalf("tick count: got %d", ev.N)
	}
}

func TestParseSetAndTotal(t *testing.T) {
	ev := mustParse(t, "set build 42")
	if ev.Kind != KindSet || ev.N != 42 {
		t.Fatalf("set: %+v", ev)
	}
	ev = mustParse(t, "total build 200")
	if ev.Kind != KindTotal || ev.N != 200 {
		t.Fatalf("total: %+v", ev)
	}
	// A set without a value is meaningless; reject it loudly.
	if _, err := ParseLine("set build"); !errors.Is(err, ErrBadNumber) {
		t.Fatalf("want ErrBadNumber, got %v", err)
	}
}

func TestParseMsgKeepsRestOfLine(t *testing.T) {
	ev := mustParse(t, "msg build cc -O2 -o main main.c")
	if ev.Kind != KindMsg || ev.Text != "cc -O2 -o main main.c" {
		t.Fatalf("msg text mangled: %+v", ev)
	}
	// Windows-authored scripts and printf '%s\r\n' both happen; the
	// parser must not leak the CR into the text field.
	if ev = mustParse(t, "msg build hello\r\n"); ev.Text != "hello" {
		t.Fatalf("CR leaked into text: %q", ev.Text)
	}
}

func TestParseDoneAndFailWithOptionalText(t *testing.T) {
	ev := mustParse(t, "done build")
	if ev.Kind != KindDone || ev.Text != "" {
		t.Fatalf("done: %+v", ev)
	}
	ev = mustParse(t, "fail test segfault in worker 3")
	if ev.Kind != KindFail || ev.Text != "segfault in worker 3" {
		t.Fatalf("fail reason lost: %+v", ev)
	}
}

func TestParseLogHasNoID(t *testing.T) {
	ev := mustParse(t, "log building on 4 cores")
	if ev.Kind != KindLog || ev.ID != "" || ev.Text != "building on 4 cores" {
		t.Fatalf("log: %+v", ev)
	}
}

func TestParseBlankAndCommentAreSkipped(t *testing.T) {
	for _, line := range []string{"", "   ", "\t", "# a comment", "  # indented comment"} {
		if _, err := ParseLine(line); !errors.Is(err, ErrSkip) {
			t.Fatalf("ParseLine(%q): want ErrSkip, got %v", line, err)
		}
	}
}

func TestParseSentinelErrors(t *testing.T) {
	// Unknown verbs are how a newer child talks to an older parent; the
	// error is a distinct sentinel so callers can tolerate it.
	if _, err := ParseLine("pause build"); !errors.Is(err, ErrUnknownVerb) {
		t.Fatalf("want ErrUnknownVerb, got %v", err)
	}
	if _, err := ParseLine("tick"); !errors.Is(err, ErrMissingID) {
		t.Fatalf("want ErrMissingID, got %v", err)
	}
}

func TestParseRejectsInvalidIDs(t *testing.T) {
	for _, line := range []string{
		"tick ba$d",         // shell metacharacter
		"tick héllo",        // non-ASCII
		"start [x] 5 label", // brackets
		"tick " + strings.Repeat("a", MaxIDLen+1),
	} {
		if _, err := ParseLine(line); err == nil {
			t.Fatalf("ParseLine(%q) should fail", line)
		}
	}
}

func TestParseRejectsNegativeAndJunkNumbers(t *testing.T) {
	for _, line := range []string{"tick build -1", "set build -5", "total build 12x", "start b 1e3"} {
		if _, err := ParseLine(line); !errors.Is(err, ErrBadNumber) {
			t.Fatalf("ParseLine(%q): want ErrBadNumber, got %v", line, err)
		}
	}
	// A tick with trailing words is almost always a typo'd msg; reject it
	// loudly rather than silently ticking by a wrong amount.
	if _, err := ParseLine("tick build 2 files"); err == nil {
		t.Fatal("trailing data after tick count must be an error")
	}
}

func TestParseLineTooLong(t *testing.T) {
	// Lines beyond PIPE_BUF lose the POSIX atomicity guarantee, so the
	// protocol refuses them outright rather than risking torn frames.
	line := "msg build " + strings.Repeat("x", MaxLineLen)
	if _, err := ParseLine(line); !errors.Is(err, ErrLineTooLong) {
		t.Fatalf("want ErrLineTooLong, got %v", err)
	}
}

func TestValidID(t *testing.T) {
	// IDs mirror what build systems produce: paths, targets, coordinates.
	valid := []string{"a", "build", "pkg/sub", "test:unit", "job-3", "a.b_c@2", strings.Repeat("z", MaxIDLen)}
	invalid := []string{"", "with space", "é", "a\tb", strings.Repeat("z", MaxIDLen+1)}
	for _, id := range valid {
		if !ValidID(id) {
			t.Errorf("ValidID(%q) = false, want true", id)
		}
		if ev := mustParse(t, "tick "+id); ev.ID != id {
			t.Errorf("id %q mangled to %q", id, ev.ID)
		}
	}
	for _, id := range invalid {
		if ValidID(id) {
			t.Errorf("ValidID(%q) = true, want false", id)
		}
	}
}

func TestFormatEventCanonicalLines(t *testing.T) {
	cases := []struct {
		ev   Event
		want string
	}{
		{Event{Kind: KindStart, ID: "b", N: 10, HasN: true, Text: "Build all"}, "start b 10 Build all"},
		{Event{Kind: KindStart, ID: "b", Text: "Pulling"}, "start b - Pulling"},
		{Event{Kind: KindStart, ID: "b"}, "start b"},
		{Event{Kind: KindTick, ID: "b", N: 1, HasN: true}, "tick b 1"},
		{Event{Kind: KindSet, ID: "b", N: 7, HasN: true}, "set b 7"},
		{Event{Kind: KindTotal, ID: "b", N: 9, HasN: true}, "total b 9"},
		{Event{Kind: KindMsg, ID: "b", Text: "cc main.c"}, "msg b cc main.c"},
		{Event{Kind: KindDone, ID: "b"}, "done b"},
		{Event{Kind: KindFail, ID: "b", Text: "oops"}, "fail b oops"},
		{Event{Kind: KindLog, Text: "hello"}, "log hello"},
		{Event{Kind: KindLog}, "log"},
	}
	for _, c := range cases {
		got, err := FormatEvent(c.ev)
		if err != nil {
			t.Fatalf("FormatEvent(%+v): %v", c.ev, err)
		}
		if got != c.want {
			t.Errorf("FormatEvent(%+v) = %q, want %q", c.ev, got, c.want)
		}
	}
	// Kind.String is load-bearing for the formatter; unknown kinds must
	// have a safe fallback rather than colliding with a real verb.
	if Kind(99).String() != "Kind(99)" {
		t.Fatalf("unknown kind fallback: %q", Kind(99).String())
	}
}

func TestFormatEventRejectsBadInput(t *testing.T) {
	bad := []Event{
		{Kind: KindTick, ID: "no id!", N: 1, HasN: true},
		{Kind: KindSet, ID: "b"},                                        // missing value
		{Kind: KindTick, ID: "b", N: -2, HasN: true},                    // negative
		{Kind: KindMsg, ID: ""},                                         // empty id
		{Kind: KindMsg, ID: "b", Text: strings.Repeat("y", MaxLineLen)}, // too long
		{Kind: Kind(42), ID: "b"},                                       // unknown kind
	}
	for _, ev := range bad {
		if _, err := FormatEvent(ev); err == nil {
			t.Errorf("FormatEvent(%+v) should fail", ev)
		}
	}
}

func TestFormatEventStripsControlCharacters(t *testing.T) {
	// A newline smuggled into a message would let one event forge
	// another; the formatter strips all control characters.
	got, err := FormatEvent(Event{Kind: KindMsg, ID: "b", Text: "a\nfail b forged\x1b[31m"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(got, "\n\x1b") {
		t.Fatalf("control characters survived: %q", got)
	}
	if got != "msg b afail b forged[31m" {
		t.Fatalf("unexpected sanitized line: %q", got)
	}
}

func TestParseFormatRoundTrip(t *testing.T) {
	// Canonical lines must survive parse→format unchanged: this is the
	// contract that makes recorded streams replayable forever.
	lines := []string{
		"start build 100 Compiling objects",
		"start pull - Downloading layers",
		"start bare",
		"tick build 3",
		"set build 42",
		"total build 200",
		"msg build linking main",
		"done build",
		"done build all green",
		"fail test exit status 2",
		"log free text with  double  spaces",
	}
	for _, line := range lines {
		ev := mustParse(t, line)
		got, err := FormatEvent(ev)
		if err != nil {
			t.Fatalf("format %q: %v", line, err)
		}
		if got != line {
			t.Errorf("round-trip changed %q -> %q", line, got)
		}
	}
}
