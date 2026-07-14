# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - 2026-07-13

### Added

- Line-oriented pipe protocol (`start` / `tick` / `set` / `total` /
  `msg` / `done` / `fail` / `log`) writable from any language with a
  bare `echo`; 512-byte line cap matching POSIX `PIPE_BUF` so concurrent
  writers never tear each other's lines; comments, blank lines, and
  unknown verbs tolerated for forward compatibility (`docs/protocol.md`).
- `barmux run -- <command>`: creates a named pipe, exports
  `BARMUX_PIPE`, renders live ANSI bars on a TTY (spinners for
  indeterminate tasks, elapsed times, in-place repaint at a bounded
  frame rate) and clean append-only milestone lines on non-TTY output;
  child stdout/stderr forwarded as log lines above the bars; child exit
  code propagated, failed tasks fail the run.
- `barmux emit`: child-side helper that validates and canonicalizes
  protocol lines, opens FIFOs non-blocking, and degrades to a silent
  no-op when no dashboard is listening — instrumented scripts run
  unchanged outside barmux.
- `barmux render [file]`: offline replay of any recorded stream, with
  `--frame` final-snapshot mode, `--step` milestone granularity,
  `--strict` malformed-line gate, and exit code 1 when a task failed.
- Dashboard state machine with implicit task creation, progress
  clamping, late totals, task restarts, and per-task/overall summaries.
- Color handling honoring `NO_COLOR` and non-TTY output; `--ascii` mode
  for terminals without Unicode; `$COLUMNS`/`--width` width control.
- Runnable examples (`examples/parallel-build.sh`, `examples/Makefile`)
  and the wire-format reference (`docs/protocol.md`).
- 92 deterministic offline tests (protocol, state, renderers, and
  in-process CLI runs against real named pipes) and `scripts/smoke.sh`.

[0.1.0]: https://github.com/JaydenCJ/barmux/releases/tag/v0.1.0
