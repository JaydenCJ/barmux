// The emit subcommand: the child-side helper. It validates its arguments
// against the protocol grammar, canonicalizes them, and writes exactly one
// line to $BARMUX_PIPE. Design rule: emit must never break the build —
// no pipe, no reader, or a full pipe all degrade to a silent no-op.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"strings"
	"syscall"

	"github.com/JaydenCJ/barmux/internal/fifo"
	"github.com/JaydenCJ/barmux/internal/protocol"
)

func (a *App) cmdEmit(args []string) int {
	fs := flag.NewFlagSet("emit", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	pipe := fs.String("pipe", "", "pipe or file to write to (default: $BARMUX_PIPE)")
	check := fs.Bool("check", false, "exit 1 instead of succeeding silently when no dashboard is listening")
	fs.Usage = func() {
		fmt.Fprintln(a.Stderr, "Usage: barmux emit [flags] <verb> [args...]")
		fmt.Fprintln(a.Stderr, "Verbs: start tick set total msg done fail log (see docs/protocol.md)")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return ExitOK // asking for help is not a usage error
		}
		return ExitUsage
	}
	if fs.NArg() == 0 {
		fs.Usage()
		return ExitUsage
	}

	// The arguments ARE a protocol line: parse them with the one true
	// grammar, then re-format canonically. `barmux emit tick build` and
	// `echo "tick build" > "$BARMUX_PIPE"` are byte-identical on the wire.
	ev, err := protocol.ParseLine(strings.Join(fs.Args(), " "))
	if err != nil {
		fmt.Fprintf(a.Stderr, "barmux emit: %v (verbs: start tick set total msg done fail log; see docs/protocol.md)\n", err)
		return ExitUsage
	}
	line, err := protocol.FormatEvent(ev)
	if err != nil {
		fmt.Fprintf(a.Stderr, "barmux emit: %v\n", err)
		return ExitUsage
	}

	path := *pipe
	if path == "" {
		path = a.getenv("BARMUX_PIPE")
	}
	if path == "" {
		// No dashboard: succeed silently so instrumented scripts run
		// unchanged outside barmux (the CI-and-bare-shell fallback).
		if *check {
			return ExitFailed
		}
		return ExitOK
	}

	f, err := fifo.OpenWrite(path)
	if err != nil {
		if errors.Is(err, fifo.ErrNoReader) {
			if *check {
				return ExitFailed
			}
			return ExitOK // parent went away; never hang or fail the build
		}
		fmt.Fprintf(a.Stderr, "barmux emit: %v\n", err)
		return ExitRuntime
	}
	defer f.Close()

	if _, err := f.WriteString(line + "\n"); err != nil {
		if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EPIPE) {
			// Pipe full (stuck parent) or reader vanished mid-write:
			// dropping a progress line beats blocking a build step.
			if *check {
				return ExitFailed
			}
			return ExitOK
		}
		fmt.Fprintf(a.Stderr, "barmux emit: write: %v\n", err)
		return ExitRuntime
	}
	return ExitOK
}
