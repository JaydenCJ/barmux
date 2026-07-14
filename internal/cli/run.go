// The run subcommand: the parent side. It creates the pipe, exports
// BARMUX_PIPE, spawns the command, and renders everything that arrives —
// protocol events from any number of (grand)children plus the command's
// own stdout/stderr, forwarded as log lines so nothing scribbles over
// the bars.
package cli

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/JaydenCJ/barmux/internal/fifo"
	"github.com/JaydenCJ/barmux/internal/protocol"
	"github.com/JaydenCJ/barmux/internal/render"
	"github.com/JaydenCJ/barmux/internal/state"
)

// sentinel is written by the parent itself (it holds a write end) after
// the child exits, so the pipe reader knows the stream is complete. It is
// a protocol comment: harmless if a stray writer ever sees it echoed.
const sentinel = "#barmux:eof"

// item is one unit of input to the render loop: a parsed event or a
// malformed-line marker.
type item struct {
	ev  protocol.Event
	bad bool
}

func (a *App) cmdRun(args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	width := fs.Int("width", a.widthDefault(), "terminal width in cells")
	noColor := fs.Bool("no-color", false, "disable ANSI colors")
	ascii := fs.Bool("ascii", false, "ASCII-only bars and spinners")
	step := fs.Int("step", 10, "percent step between plain milestones")
	fps := fs.Int("fps", 10, "live repaints per second (TTY only)")
	pipePath := fs.String("pipe", "", "create the pipe at this path instead of a temp dir")
	fs.Usage = func() {
		fmt.Fprintln(a.Stderr, "Usage: barmux run [flags] -- <command> [args...]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return ExitOK // asking for help is not a usage error
		}
		return ExitUsage
	}
	cmdArgs := fs.Args()
	if len(cmdArgs) > 0 && cmdArgs[0] == "--" {
		cmdArgs = cmdArgs[1:]
	}
	if len(cmdArgs) == 0 {
		fmt.Fprintln(a.Stderr, "barmux run: no command given")
		fs.Usage()
		return ExitUsage
	}
	if *fps < 1 {
		*fps = 1
	} else if *fps > 60 {
		*fps = 60
	}

	// 1. Pipe setup.
	path := *pipePath
	if path == "" {
		dir, err := os.MkdirTemp("", "barmux-")
		if err != nil {
			fmt.Fprintf(a.Stderr, "barmux run: %v\n", err)
			return ExitRuntime
		}
		defer os.RemoveAll(dir)
		path = filepath.Join(dir, "barmux.pipe")
	}
	if err := fifo.Create(path); err != nil {
		fmt.Fprintf(a.Stderr, "barmux run: create pipe: %v\n", err)
		return ExitRuntime
	}
	if *pipePath != "" {
		defer os.Remove(path)
	}
	pipe, err := fifo.OpenRead(path)
	if err != nil {
		fmt.Fprintf(a.Stderr, "barmux run: open pipe: %v\n", err)
		return ExitRuntime
	}
	defer pipe.Close()

	// 2. Child setup: BARMUX_PIPE in the environment, stdout/stderr
	// captured line-wise and turned into log events.
	cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
	cmd.Env = append(os.Environ(), "BARMUX_PIPE="+path)
	cmd.Stdin = a.Stdin
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		fmt.Fprintf(a.Stderr, "barmux run: %v\n", err)
		return ExitRuntime
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		fmt.Fprintf(a.Stderr, "barmux run: %v\n", err)
		return ExitRuntime
	}

	events := make(chan item, 256)
	var readers sync.WaitGroup // pipe reader + 2 log readers
	var logs sync.WaitGroup    // just the 2 log readers (gate for cmd.Wait)

	readers.Add(1)
	go func() {
		defer readers.Done()
		readPipe(pipe, events)
	}()
	for _, r := range []io.Reader{stdout, stderr} {
		readers.Add(1)
		logs.Add(1)
		go func(r io.Reader) {
			defer readers.Done()
			defer logs.Done()
			readLogs(r, events)
		}(r)
	}

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(a.Stderr, "barmux run: %v\n", err)
		// Unblock the pipe reader so the goroutines exit cleanly.
		fmt.Fprintln(pipe, sentinel)
		readers.Wait()
		return ExitRuntime
	}

	// Forward interrupts to the child; the child decides when to die and
	// we keep rendering until it does.
	sigc := make(chan os.Signal, 2)
	signal.Notify(sigc, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigc)
	go func() {
		for s := range sigc {
			if cmd.Process != nil {
				_ = cmd.Process.Signal(s)
			}
		}
	}()

	// 3. Reap the child once its output is fully drained, then release
	// the pipe reader via the sentinel so the events channel closes.
	waitErr := make(chan error, 1)
	go func() {
		logs.Wait()
		err := cmd.Wait()
		fmt.Fprintln(pipe, sentinel)
		readers.Wait()
		close(events)
		waitErr <- err
	}()

	// 4. Render until the stream completes.
	opts := render.Options{
		Width: *width,
		Color: a.colorDefault() && !*noColor,
		ASCII: *ascii,
		Step:  *step,
	}
	d := state.New(a.now())
	if a.isTTY() {
		a.liveLoop(d, events, opts, *fps)
	} else {
		a.plainLoop(d, events, opts)
	}

	// 5. Exit code: the child's own code wins; a clean child with failed
	// tasks still fails the run, so `barmux run` is safe in scripts.
	if err := <-waitErr; err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			if code := ee.ExitCode(); code > 0 {
				return code
			}
			return ExitRuntime // killed by a signal
		}
		fmt.Fprintf(a.Stderr, "barmux run: %v\n", err)
		return ExitRuntime
	}
	if d.Summary().Failed > 0 {
		return ExitFailed
	}
	return ExitOK
}

// readPipe scans protocol lines from the named pipe until the sentinel.
func readPipe(r io.Reader, events chan<- item) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 4096), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, sentinel) {
			return
		}
		ev, err := protocol.ParseLine(line)
		if err != nil {
			if err != protocol.ErrSkip {
				events <- item{bad: true}
			}
			continue
		}
		events <- item{ev: ev}
	}
}

// readLogs forwards each line of the child's stdout/stderr as a log event.
func readLogs(r io.Reader, events chan<- item) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 4096), 1024*1024)
	for sc.Scan() {
		events <- item{ev: protocol.Event{Kind: protocol.KindLog, Text: sc.Text()}}
	}
}

// plainLoop is the non-TTY render loop: apply every event, stream
// milestones, one final summary. This is what CI sees.
func (a *App) plainLoop(d *state.Dashboard, events <-chan item, opts render.Options) {
	plain := render.NewPlain(a.Stdout, d, opts)
	for it := range events {
		if it.bad {
			d.CountMalformed()
			continue
		}
		d.Apply(it.ev)
		plain.Handle(it.ev)
	}
	plain.Summary()
}
