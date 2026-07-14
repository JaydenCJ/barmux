//go:build unix

// End-to-end tests for the run command: a real named pipe, real /bin/sh
// children, in-process App. Everything is event-driven (the loop ends at
// the sentinel, not on a timer), so these are deterministic and fast.
package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// runApp executes `barmux run -- sh -c script` hermetically.
func runApp(t *testing.T, script string, extra ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	clock := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	a := &App{
		Stdin:  strings.NewReader(""),
		Stdout: &out,
		Stderr: &errb,
		Getenv: func(string) string { return "" },
		IsTTY:  func() bool { return false },
		Now:    func() time.Time { clock = clock.Add(time.Second); return clock },
	}
	args := append([]string{"run"}, extra...)
	args = append(args, "--", "sh", "-c", script)
	code := a.Main(args)
	return code, out.String(), errb.String()
}

func TestRunHappyPathExportsPipeAndRendersMilestones(t *testing.T) {
	code, out, errb := runApp(t, `
		echo "log pipe is $BARMUX_PIPE" > "$BARMUX_PIPE"
		echo "start lint 4 Linting sources" > "$BARMUX_PIPE"
		echo "tick lint 2" > "$BARMUX_PIPE"
		echo "tick lint 2" > "$BARMUX_PIPE"
		echo "done lint" > "$BARMUX_PIPE"
	`)
	if code != ExitOK {
		t.Fatalf("exit %d (stderr %q)", code, errb)
	}
	for _, want := range []string{
		"pipe is /", // BARMUX_PIPE was exported and absolute
		"[lint] start: Linting sources (0/4)",
		"[lint]  50% (2/4)",
		"[lint] 100% (4/4)",
		"[lint] done (4/4)",
		"1 task · 1 done · overall 100%",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestRunForwardsChildStdoutAndStderrAsLogs(t *testing.T) {
	code, out, _ := runApp(t, `
		echo "plain stdout line"
		echo "plain stderr line" >&2
	`)
	if code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out, "plain stdout line") || !strings.Contains(out, "plain stderr line") {
		t.Fatalf("child output not forwarded:\n%s", out)
	}
}

func TestRunFailedTaskExitsOneEvenIfChildExitsZero(t *testing.T) {
	code, out, _ := runApp(t, `echo "fail deploy bad credentials" > "$BARMUX_PIPE"`)
	if code != ExitFailed {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out, "[deploy] FAIL: bad credentials") {
		t.Fatalf("fail line missing:\n%s", out)
	}
}

func TestRunPropagatesChildExitCode(t *testing.T) {
	code, _, _ := runApp(t, `exit 7`)
	if code != 7 {
		t.Fatalf("child exit code lost: %d", code)
	}
}

func TestRunManyConcurrentWriters(t *testing.T) {
	// Four subshells hammer the pipe concurrently; per-line atomicity
	// (PIPE_BUF) means every event survives intact and the final counts
	// are exact.
	code, out, _ := runApp(t, `
		for j in a b c d; do
			(
				echo "start $j 25 Job $j" > "$BARMUX_PIPE"
				i=0
				while [ $i -lt 25 ]; do
					echo "tick $j" > "$BARMUX_PIPE"
					i=$((i+1))
				done
				echo "done $j" > "$BARMUX_PIPE"
			) &
		done
		wait
	`)
	if code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	for _, j := range []string{"a", "b", "c", "d"} {
		if !strings.Contains(out, "["+j+"] done (25/25)") {
			t.Fatalf("job %s incomplete:\n%s", j, out)
		}
	}
	if !strings.Contains(out, "4 tasks · 4 done · overall 100%") {
		t.Fatalf("summary wrong:\n%s", out)
	}
}

func TestRunMalformedLinesCountedNotFatal(t *testing.T) {
	code, out, _ := runApp(t, `
		echo "start b 2" > "$BARMUX_PIPE"
		echo "complete garbage !!" > "$BARMUX_PIPE"
		echo "tick b 2" > "$BARMUX_PIPE"
		echo "done b" > "$BARMUX_PIPE"
	`)
	if code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out, "1 malformed line") {
		t.Fatalf("malformed not surfaced:\n%s", out)
	}
	if !strings.Contains(out, "[b] done (2/2)") {
		t.Fatalf("stream after garbage lost:\n%s", out)
	}
}

func TestRunExplicitPipePathIsCreatedAndRemoved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "custom.pipe")
	code, _, _ := runApp(t, `echo "log hi" > "$BARMUX_PIPE"`, "--pipe", path)
	if code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("explicit pipe should be removed after the run")
	}
}

func TestRunUsageAndSpawnErrors(t *testing.T) {
	mk := func() (*App, *bytes.Buffer) {
		var out, errb bytes.Buffer
		return &App{Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errb,
			Getenv: func(string) string { return "" }, IsTTY: func() bool { return false }}, &errb
	}
	a, errb := mk()
	if code := a.Main([]string{"run", "--"}); code != ExitUsage {
		t.Fatalf("no command: exit %d", code)
	}
	if !strings.Contains(errb.String(), "no command") {
		t.Fatalf("stderr: %q", errb.String())
	}
	a, errb = mk()
	if code := a.Main([]string{"run", "--", "/nonexistent/binary-xyz"}); code != ExitRuntime {
		t.Fatalf("missing binary: exit %d (stderr %q)", code, errb.String())
	}
}

func TestRunBatchedWritesAndLogOrdering(t *testing.T) {
	// printf batching several protocol lines into one write is a common
	// shell idiom; the reader must split them correctly, multi-word
	// labels must survive end to end, and log lines must appear in
	// stream order relative to milestones.
	code, out, _ := runApp(t, `
		printf 'start docs 2 Rendering API docs\nlog checkpoint one\n' > "$BARMUX_PIPE"
		printf 'tick docs 2\ndone docs\n' > "$BARMUX_PIPE"
	`)
	if code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out, "[docs] start: Rendering API docs (0/2)") {
		t.Fatalf("label lost:\n%s", out)
	}
	idx1 := strings.Index(out, "checkpoint one")
	idx2 := strings.Index(out, "[docs] done")
	if idx1 < 0 || idx2 < 0 || idx1 > idx2 {
		t.Fatalf("log line misplaced:\n%s", out)
	}
}
