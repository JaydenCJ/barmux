// In-process CLI tests: every command runs against injected buffers and a
// fake environment, so parsing, rendering, exit codes, and the emit
// fallback chain are all exercised without touching a real terminal.
package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// app builds a hermetic App writing to fresh buffers. env maps the fake
// environment; tty forces the TTY answer.
func app(env map[string]string, tty bool) (*App, *bytes.Buffer, *bytes.Buffer) {
	var out, errb bytes.Buffer
	clock := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	return &App{
		Stdin:  strings.NewReader(""),
		Stdout: &out,
		Stderr: &errb,
		Getenv: func(k string) string { return env[k] },
		IsTTY:  func() bool { return tty },
		Now:    func() time.Time { clock = clock.Add(time.Second); return clock },
	}, &out, &errb
}

const stream = `start build 10 Compiling objects
start test - Running tests
tick build 5
msg build cc main.c
tick test 3
set build 10
done build
done test all green
`

func TestVersionCommandAndAliases(t *testing.T) {
	a, out, _ := app(nil, false)
	if code := a.Main([]string{"version"}); code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	if out.String() != "barmux 0.1.0\n" {
		t.Fatalf("version output: %q", out.String())
	}
	for _, arg := range []string{"--version", "-V"} {
		a, out, _ := app(nil, false)
		if code := a.Main([]string{arg}); code != ExitOK || !strings.Contains(out.String(), "0.1.0") {
			t.Fatalf("%s: exit %d, out %q", arg, code, out.String())
		}
	}
}

func TestHelpAndUsageErrors(t *testing.T) {
	a, out, _ := app(nil, false)
	if code := a.Main([]string{"--help"}); code != ExitOK {
		t.Fatalf("help exit %d", code)
	}
	if !strings.Contains(out.String(), "barmux run") {
		t.Fatal("usage text missing")
	}
	// No arguments and unknown commands are usage errors on stderr.
	a, _, errb := app(nil, false)
	if code := a.Main(nil); code != ExitUsage || !strings.Contains(errb.String(), "Usage") {
		t.Fatalf("no args: exit %d, stderr %q", code, errb.String())
	}
	a, _, errb = app(nil, false)
	if code := a.Main([]string{"frobnicate"}); code != ExitUsage || !strings.Contains(errb.String(), "unknown command") {
		t.Fatalf("unknown cmd: exit %d, stderr %q", code, errb.String())
	}
}

func TestSubcommandHelpExitsZero(t *testing.T) {
	// --help is a request, not a mistake: unlike genuine usage errors it
	// must exit 0, so scripted `barmux <cmd> --help` probes stay green.
	for _, cmd := range []string{"run", "render", "emit"} {
		a, _, errb := app(nil, false)
		if code := a.Main([]string{cmd, "--help"}); code != ExitOK {
			t.Fatalf("%s --help: exit %d", cmd, code)
		}
		if !strings.Contains(errb.String(), "Usage: barmux "+cmd) {
			t.Fatalf("%s --help: usage text missing: %q", cmd, errb.String())
		}
	}
}

func TestRenderStreamFromStdin(t *testing.T) {
	a, out, _ := app(nil, false)
	a.Stdin = strings.NewReader(stream)
	if code := a.Main([]string{"render"}); code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	got := out.String()
	for _, want := range []string{
		"[build] start: Compiling objects (0/10)",
		"[build]  50% (5/10)",
		"[build] done (10/10)",
		"[test] done (3 items)",
		"2 tasks · 2 done · overall 100%",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
}

func TestRenderFileArgumentHandling(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.log")
	if err := os.WriteFile(path, []byte(stream), 0o644); err != nil {
		t.Fatal(err)
	}
	a, out, _ := app(nil, false)
	if code := a.Main([]string{"render", path}); code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out.String(), "[build] done (10/10)") {
		t.Fatalf("file input not rendered:\n%s", out.String())
	}
	// Unreadable file: runtime error. Two files: usage error.
	a, _, errb := app(nil, false)
	if code := a.Main([]string{"render", "/nonexistent/trace.log"}); code != ExitRuntime {
		t.Fatalf("missing file exit %d (stderr %q)", code, errb.String())
	}
	a, _, _ = app(nil, false)
	if code := a.Main([]string{"render", "a.log", "b.log"}); code != ExitUsage {
		t.Fatalf("two files exit %d", code)
	}
}

func TestRenderFailedTaskExitsOne(t *testing.T) {
	a, out, _ := app(nil, false)
	a.Stdin = strings.NewReader("start d 4\nfail d disk full\n")
	if code := a.Main([]string{"render"}); code != ExitFailed {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out.String(), "[d] FAIL: disk full") {
		t.Fatalf("fail line missing:\n%s", out.String())
	}
}

func TestRenderTolerantOfMalformedLinesByDefault(t *testing.T) {
	a, out, _ := app(nil, false)
	a.Stdin = strings.NewReader("garbage here\nstart b 2\ntick b 2\ndone b\nalso bad\n")
	if code := a.Main([]string{"render"}); code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out.String(), "2 malformed lines") {
		t.Fatalf("malformed count missing:\n%s", out.String())
	}
	// Comments and blank lines are protocol, not garbage.
	a, out, _ = app(nil, false)
	a.Stdin = strings.NewReader("# header comment\n\nstart b 1\ntick b\ndone b\n")
	if code := a.Main([]string{"render"}); code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	if strings.Contains(out.String(), "malformed") {
		t.Fatalf("comments must not count as malformed:\n%s", out.String())
	}
}

func TestRenderStrictAbortsOnMalformed(t *testing.T) {
	a, _, errb := app(nil, false)
	a.Stdin = strings.NewReader("start b 2\nnonsense\n")
	if code := a.Main([]string{"render", "--strict"}); code != ExitRuntime {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(errb.String(), "line 2") {
		t.Fatalf("strict error should cite the line: %q", errb.String())
	}
}

func TestRenderFrameSnapshot(t *testing.T) {
	a, out, _ := app(nil, false)
	a.Stdin = strings.NewReader(stream)
	if code := a.Main([]string{"render", "--frame", "--width", "100"}); code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	got := out.String()
	if strings.Contains(got, "[build]") {
		t.Fatalf("--frame must not stream milestones:\n%s", got)
	}
	for _, want := range []string{"✔", "Compiling objects", "100% (10/10)", "2 tasks"} {
		if !strings.Contains(got, want) {
			t.Fatalf("frame missing %q:\n%s", want, got)
		}
	}
}

func TestRenderQuietOnlySummary(t *testing.T) {
	a, out, _ := app(nil, false)
	a.Stdin = strings.NewReader(stream)
	if code := a.Main([]string{"render", "--quiet"}); code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	if got := strings.Count(out.String(), "\n"); got != 1 {
		t.Fatalf("quiet should print exactly the summary:\n%s", out.String())
	}
}

func TestRenderColorRules(t *testing.T) {
	// Non-TTY output must never contain ANSI colors.
	a, out, _ := app(nil, false)
	a.Stdin = strings.NewReader(stream)
	a.Main([]string{"render", "--frame"})
	if strings.Contains(out.String(), "\x1b[") {
		t.Fatal("non-TTY output must not contain ANSI colors")
	}
	// A TTY gets color...
	a, out, _ = app(nil, true)
	a.Stdin = strings.NewReader(stream)
	a.Main([]string{"render", "--frame"})
	if !strings.Contains(out.String(), "\x1b[") {
		t.Fatal("TTY frame should be colored")
	}
	// ...unless NO_COLOR (https://no-color.org) is set.
	a, out, _ = app(map[string]string{"NO_COLOR": "1"}, true)
	a.Stdin = strings.NewReader(stream)
	a.Main([]string{"render", "--frame"})
	if strings.Contains(out.String(), "\x1b[") {
		t.Fatal("NO_COLOR must disable colors even on a TTY")
	}
}

func TestRenderWidthFromColumnsEnv(t *testing.T) {
	a, out, _ := app(map[string]string{"COLUMNS": "40"}, false)
	a.Stdin = strings.NewReader("start b 10 A very long label indeed for forty cells\ntick b 5\n")
	a.Main([]string{"render", "--frame"})
	for _, line := range strings.Split(strings.TrimRight(out.String(), "\n"), "\n") {
		if n := len([]rune(line)); n > 40 {
			t.Fatalf("line wider than $COLUMNS (%d): %q", n, line)
		}
	}
}

func TestEmitWritesCanonicalLinesToPipeFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.log")
	env := map[string]string{"BARMUX_PIPE": path}
	a, _, _ := app(env, false)
	if code := a.Main([]string{"emit", "start", "build", "10", "Compiling", "objects"}); code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	a, _, _ = app(env, false)
	if code := a.Main([]string{"emit", "log", "hello", "from", "a", "script"}); code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "start build 10 Compiling objects\nlog hello from a script\n" {
		t.Fatalf("wire lines: %q", got)
	}
}

func TestEmitAppendsAcrossInvocations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.log")
	env := map[string]string{"BARMUX_PIPE": path}
	for _, args := range [][]string{
		{"emit", "start", "b", "2"},
		{"emit", "tick", "b"},
		{"emit", "done", "b"},
	} {
		a, _, _ := app(env, false)
		if code := a.Main(args); code != ExitOK {
			t.Fatalf("%v: exit %d", args, code)
		}
	}
	got, _ := os.ReadFile(path)
	if string(got) != "start b 2\ntick b 1\ndone b\n" {
		t.Fatalf("trace: %q", got)
	}
}

func TestEmitPipeFlagOverridesEnv(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, "env.log")
	flagPath := filepath.Join(dir, "flag.log")
	a, _, _ := app(map[string]string{"BARMUX_PIPE": envPath}, false)
	if code := a.Main([]string{"emit", "--pipe", flagPath, "tick", "b"}); code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	if _, err := os.Stat(envPath); !os.IsNotExist(err) {
		t.Fatal("env path should be untouched when --pipe is given")
	}
	if _, err := os.Stat(flagPath); err != nil {
		t.Fatal("flag path should be written")
	}
}

func TestEmitDegradesWithoutListener(t *testing.T) {
	// The core degradation contract: instrumented scripts run unchanged
	// when no dashboard is listening — silent, successful, zero output.
	a, out, errb := app(nil, false)
	if code := a.Main([]string{"emit", "tick", "build"}); code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	if out.Len() != 0 || errb.Len() != 0 {
		t.Fatalf("must be silent: out=%q err=%q", out.String(), errb.String())
	}
	// --check opts into a detectable "nobody is listening" exit code.
	a, _, _ = app(nil, false)
	if code := a.Main([]string{"emit", "--check", "tick", "build"}); code != ExitFailed {
		t.Fatalf("--check exit %d", code)
	}
}

func TestEmitInvalidProtocolExitsTwo(t *testing.T) {
	for _, args := range [][]string{
		{"emit", "warp", "b"},          // unknown verb
		{"emit", "tick", "no spaces"},  // invalid id
		{"emit", "set", "b", "twelve"}, // bad number
		{"emit"},                       // nothing at all
	} {
		a, _, errb := app(nil, false)
		if code := a.Main(args); code != ExitUsage {
			t.Fatalf("%v: exit %d (stderr %q)", args, code, errb.String())
		}
	}
}
