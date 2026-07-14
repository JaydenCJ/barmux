// Package protocol implements the barmux wire protocol: a line-oriented,
// echo-friendly text format that child processes write to a pipe and the
// parent parses into events.
//
// The protocol is deliberately dumb. One event per line, fields separated
// by whitespace, the trailing field is free text ("rest of line"), so a
// shell script can participate with nothing more than:
//
//	echo "start build 100 Compiling objects" > "$BARMUX_PIPE"
//	echo "tick build" > "$BARMUX_PIPE"
//	echo "done build" > "$BARMUX_PIPE"
//
// Writes of a full line up to 512 bytes are atomic on POSIX pipes
// (PIPE_BUF), which is why MaxLineLen is capped there: concurrent writers
// never tear each other's lines. See docs/protocol.md for the full spec.
package protocol

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Kind identifies the verb of a protocol event.
type Kind int

// The set of verbs understood by barmux 0.1.0. Parsers must tolerate
// unknown verbs (ErrUnknownVerb) so older parents survive newer children.
const (
	KindStart Kind = iota // start <id> [<total>|-] [label...]
	KindTick              // tick <id> [n]
	KindSet               // set <id> <current>
	KindTotal             // total <id> <total>
	KindMsg               // msg <id> [text...]
	KindDone              // done <id> [text...]
	KindFail              // fail <id> [text...]
	KindLog               // log [text...]
)

// String returns the wire verb for a Kind.
func (k Kind) String() string {
	switch k {
	case KindStart:
		return "start"
	case KindTick:
		return "tick"
	case KindSet:
		return "set"
	case KindTotal:
		return "total"
	case KindMsg:
		return "msg"
	case KindDone:
		return "done"
	case KindFail:
		return "fail"
	case KindLog:
		return "log"
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}

// Event is one parsed protocol line.
type Event struct {
	Kind Kind
	ID   string // task identifier; empty for log events
	N    int64  // tick delta, set value, or total, when HasN
	HasN bool   // whether N carries a value (start without total: false)
	Text string // label (start), message (msg/done/fail), or log text
}

// Limits enforced by the parser. MaxLineLen matches the POSIX PIPE_BUF
// atomicity guarantee; MaxIDLen keeps dashboards readable.
const (
	MaxLineLen = 512
	MaxIDLen   = 64
)

// Sentinel errors. ErrSkip is returned for blank lines and '#' comments —
// callers drop those silently. Everything else counts as malformed input.
var (
	ErrSkip        = errors.New("skippable line")
	ErrEmptyVerb   = errors.New("empty verb")
	ErrUnknownVerb = errors.New("unknown verb")
	ErrMissingID   = errors.New("missing task id")
	ErrBadID       = errors.New("invalid task id")
	ErrBadNumber   = errors.New("invalid number")
	ErrLineTooLong = errors.New("line exceeds 512 bytes")
)

// ValidID reports whether id is a legal task identifier:
// 1–64 characters from [A-Za-z0-9._:@/-].
func ValidID(id string) bool {
	if id == "" || len(id) > MaxIDLen {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= 'a' && c <= 'z':
		case c >= 'A' && c <= 'Z':
		case c >= '0' && c <= '9':
		case c == '.' || c == '_' || c == ':' || c == '@' || c == '/' || c == '-':
		default:
			return false
		}
	}
	return true
}

// cut splits s at the first run of spaces or tabs, returning the head and
// the trimmed remainder. Protocol fields are whitespace-separated; the
// final field is always "rest of line" so it may contain spaces.
func cut(s string) (head, rest string) {
	s = strings.TrimLeft(s, " \t")
	i := strings.IndexAny(s, " \t")
	if i < 0 {
		return s, ""
	}
	return s[:i], strings.TrimLeft(s[i:], " \t")
}

// parseCount parses a non-negative int64 protocol number.
func parseCount(s string) (int64, error) {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%w: %q", ErrBadNumber, s)
	}
	return n, nil
}

// ParseLine parses one protocol line into an Event.
//
// Returns ErrSkip for blank lines and lines starting with '#'; callers
// should silently ignore those. Any other error means the line is
// malformed and should be counted (and, under --strict, fatal).
func ParseLine(line string) (Event, error) {
	if len(line) > MaxLineLen {
		return Event{}, ErrLineTooLong
	}
	line = strings.TrimRight(line, "\r\n")
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return Event{}, ErrSkip
	}

	verb, rest := cut(trimmed)
	switch verb {
	case "log":
		return Event{Kind: KindLog, Text: rest}, nil
	case "start", "tick", "set", "total", "msg", "done", "fail":
		// all remaining verbs require an id
	default:
		return Event{}, fmt.Errorf("%w: %q", ErrUnknownVerb, verb)
	}

	id, rest := cut(rest)
	if id == "" {
		return Event{}, fmt.Errorf("%w after %q", ErrMissingID, verb)
	}
	if !ValidID(id) {
		return Event{}, fmt.Errorf("%w: %q", ErrBadID, id)
	}

	switch verb {
	case "start":
		ev := Event{Kind: KindStart, ID: id}
		totalField, label := cut(rest)
		switch totalField {
		case "", "-":
			// indeterminate task; "-" is the explicit spelling so a
			// label can still follow: `start pull - Downloading layers`
			ev.Text = label
		default:
			n, err := parseCount(totalField)
			if err != nil {
				return Event{}, fmt.Errorf("start total: %w", err)
			}
			ev.N, ev.HasN = n, true
			ev.Text = label
		}
		return ev, nil

	case "tick":
		ev := Event{Kind: KindTick, ID: id, N: 1, HasN: true}
		if rest != "" {
			field, trailing := cut(rest)
			if trailing != "" {
				return Event{}, fmt.Errorf("tick: trailing data %q", trailing)
			}
			n, err := parseCount(field)
			if err != nil {
				return Event{}, fmt.Errorf("tick: %w", err)
			}
			ev.N = n
		}
		return ev, nil

	case "set", "total":
		kind := KindSet
		if verb == "total" {
			kind = KindTotal
		}
		field, trailing := cut(rest)
		if field == "" {
			return Event{}, fmt.Errorf("%s: %w: missing value", verb, ErrBadNumber)
		}
		if trailing != "" {
			return Event{}, fmt.Errorf("%s: trailing data %q", verb, trailing)
		}
		n, err := parseCount(field)
		if err != nil {
			return Event{}, fmt.Errorf("%s: %w", verb, err)
		}
		return Event{Kind: kind, ID: id, N: n, HasN: true}, nil

	case "msg":
		return Event{Kind: KindMsg, ID: id, Text: rest}, nil
	case "done":
		return Event{Kind: KindDone, ID: id, Text: rest}, nil
	case "fail":
		return Event{Kind: KindFail, ID: id, Text: rest}, nil
	}
	// unreachable: every verb is handled above
	return Event{}, fmt.Errorf("%w: %q", ErrUnknownVerb, verb)
}

// sanitizeText strips control characters (including newlines, which would
// break line framing) from free-text fields when formatting events.
func sanitizeText(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
}

// FormatEvent renders an Event back into its canonical wire line (no
// trailing newline). It is the inverse of ParseLine for events that
// round-trip: barmux emit uses it so scripts never hand-format lines.
func FormatEvent(e Event) (string, error) {
	if e.Kind != KindLog {
		if !ValidID(e.ID) {
			return "", fmt.Errorf("%w: %q", ErrBadID, e.ID)
		}
	}
	text := sanitizeText(e.Text)

	var b strings.Builder
	b.WriteString(e.Kind.String())
	switch e.Kind {
	case KindLog:
		if text != "" {
			b.WriteByte(' ')
			b.WriteString(text)
		}
	case KindStart:
		b.WriteByte(' ')
		b.WriteString(e.ID)
		if e.HasN {
			if e.N < 0 {
				return "", fmt.Errorf("%w: %d", ErrBadNumber, e.N)
			}
			fmt.Fprintf(&b, " %d", e.N)
		} else if text != "" {
			b.WriteString(" -")
		}
		if text != "" {
			b.WriteByte(' ')
			b.WriteString(text)
		}
	case KindTick, KindSet, KindTotal:
		if !e.HasN || e.N < 0 {
			return "", fmt.Errorf("%s: %w: %d", e.Kind, ErrBadNumber, e.N)
		}
		fmt.Fprintf(&b, " %s %d", e.ID, e.N)
	case KindMsg, KindDone, KindFail:
		b.WriteByte(' ')
		b.WriteString(e.ID)
		if text != "" {
			b.WriteByte(' ')
			b.WriteString(text)
		}
	default:
		return "", fmt.Errorf("%w: %v", ErrUnknownVerb, e.Kind)
	}

	line := b.String()
	if len(line) > MaxLineLen {
		return "", ErrLineTooLong
	}
	return line, nil
}
