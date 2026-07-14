// Package cli implements the barmux command line: run, render, emit,
// version. All I/O is injected through App so every command is testable
// in-process with plain byte buffers.
package cli

import (
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/JaydenCJ/barmux/internal/version"
)

// Exit codes, documented in the README:
//
//	0 success (and, for run, the child exited 0)
//	1 a task failed (or, for run, the child's own non-zero code)
//	2 usage error
//	3 runtime error (unreadable input, pipe setup failure, ...)
const (
	ExitOK      = 0
	ExitFailed  = 1
	ExitUsage   = 2
	ExitRuntime = 3
)

// App bundles the process environment a command needs, so tests can run
// commands hermetically.
type App struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	Getenv func(string) string // nil-safe: nil means "empty environment"
	IsTTY  func() bool         // is stdout a live terminal?
	Now    func() time.Time    // clock for elapsed times; nil means time.Now
}

func (a *App) getenv(key string) string {
	if a.Getenv == nil {
		return ""
	}
	return a.Getenv(key)
}

func (a *App) isTTY() bool {
	return a.IsTTY != nil && a.IsTTY()
}

func (a *App) now() func() time.Time {
	if a.Now != nil {
		return a.Now
	}
	return time.Now
}

const usage = `barmux — one progress dashboard for many processes

Usage:
  barmux run [flags] -- <command> [args...]   run a command with a dashboard
  barmux render [flags] [file]                render a protocol stream (stdin or file)
  barmux emit [flags] <verb> [args...]        write one protocol line to $BARMUX_PIPE
  barmux version                              print version

Flags shared by run and render:
  --width N     terminal width (default: $COLUMNS, else 80)
  --no-color    disable ANSI colors (NO_COLOR is also honored)
  --ascii       ASCII-only bars and spinners
  --step N      percent step between plain-mode milestones (default 10)

Run 'barmux <command> --help' for the full per-command flag list.
Protocol reference: docs/protocol.md ('start id total label', 'tick id', ...).
`

// Main dispatches argv (without the program name) and returns the exit code.
func (a *App) Main(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(a.Stderr, usage)
		return ExitUsage
	}
	switch args[0] {
	case "run":
		return a.cmdRun(args[1:])
	case "render":
		return a.cmdRender(args[1:])
	case "emit":
		return a.cmdEmit(args[1:])
	case "version", "--version", "-V":
		fmt.Fprintf(a.Stdout, "barmux %s\n", version.Version)
		return ExitOK
	case "help", "--help", "-h":
		fmt.Fprint(a.Stdout, usage)
		return ExitOK
	default:
		fmt.Fprintf(a.Stderr, "barmux: unknown command %q\n\n", args[0])
		fmt.Fprint(a.Stderr, usage)
		return ExitUsage
	}
}

// colorDefault decides whether color is on before flags are applied:
// TTY output and no NO_COLOR (https://no-color.org) in the environment.
func (a *App) colorDefault() bool {
	return a.isTTY() && a.getenv("NO_COLOR") == ""
}

// widthDefault resolves the terminal width: $COLUMNS if it parses,
// otherwise the render package default (80).
func (a *App) widthDefault() int {
	if v := a.getenv("COLUMNS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 0 // render.Options treats <=0 as DefaultWidth
}
