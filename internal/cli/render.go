// The render subcommand: consume a protocol stream from a file or stdin
// and render it. Non-TTY output (the default in CI and in tests) streams
// plain milestone lines; --frame prints a single final dashboard snapshot
// instead. This is also the offline replay tool: any recorded stream is
// re-renderable forever.
package cli

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/JaydenCJ/barmux/internal/protocol"
	"github.com/JaydenCJ/barmux/internal/render"
	"github.com/JaydenCJ/barmux/internal/state"
)

func (a *App) cmdRender(args []string) int {
	fs := flag.NewFlagSet("render", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	width := fs.Int("width", a.widthDefault(), "terminal width in cells")
	noColor := fs.Bool("no-color", false, "disable ANSI colors")
	ascii := fs.Bool("ascii", false, "ASCII-only bars and spinners")
	step := fs.Int("step", 10, "percent step between plain milestones")
	strict := fs.Bool("strict", false, "fail on the first malformed protocol line")
	frame := fs.Bool("frame", false, "print one final dashboard frame instead of streaming")
	quiet := fs.Bool("quiet", false, "suppress milestones; only the final summary")
	fs.Usage = func() {
		fmt.Fprintln(a.Stderr, "Usage: barmux render [flags] [file]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return ExitOK // asking for help is not a usage error
		}
		return ExitUsage
	}
	if fs.NArg() > 1 {
		fmt.Fprintln(a.Stderr, "barmux render: at most one input file")
		return ExitUsage
	}

	in := a.Stdin
	if fs.NArg() == 1 {
		f, err := os.Open(fs.Arg(0))
		if err != nil {
			fmt.Fprintf(a.Stderr, "barmux render: %v\n", err)
			return ExitRuntime
		}
		defer f.Close()
		in = f
	}

	opts := render.Options{
		Width: *width,
		Color: a.colorDefault() && !*noColor,
		ASCII: *ascii,
		Step:  *step,
	}
	d := state.New(a.now())
	var plain *render.Plain
	if !*frame && !*quiet {
		plain = render.NewPlain(a.Stdout, d, opts)
	}

	if code := a.consume(in, d, plain, *strict); code != ExitOK {
		return code
	}

	if *frame {
		fmt.Fprint(a.Stdout, render.Frame(d, 0, a.now()(), opts))
	} else {
		fmt.Fprintln(a.Stdout, render.SummaryLine(d.Summary(), opts))
	}
	if d.Summary().Failed > 0 {
		return ExitFailed
	}
	return ExitOK
}

// consume reads protocol lines from in, applying them to d and echoing
// them through the optional plain renderer. Malformed lines are counted
// (and reported to stderr under --strict, which aborts).
func (a *App) consume(in io.Reader, d *state.Dashboard, plain *render.Plain, strict bool) int {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 4096), 1024*1024)
	lineno := 0
	for sc.Scan() {
		lineno++
		ev, err := protocol.ParseLine(sc.Text())
		if err != nil {
			if errors.Is(err, protocol.ErrSkip) {
				continue
			}
			d.CountMalformed()
			if strict {
				fmt.Fprintf(a.Stderr, "barmux render: line %d: %v\n", lineno, err)
				return ExitRuntime
			}
			continue
		}
		d.Apply(ev)
		if plain != nil {
			plain.Handle(ev)
		} else if ev.Kind == protocol.KindLog {
			d.DrainLogs() // keep the pending-log buffer bounded in quiet/frame mode
		}
	}
	if err := sc.Err(); err != nil {
		fmt.Fprintf(a.Stderr, "barmux render: read: %v\n", err)
		return ExitRuntime
	}
	return ExitOK
}
