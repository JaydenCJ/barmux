# Contributing to barmux

Issues, discussions and pull requests are all welcome.

## Getting started

You need Go ≥1.22 and a POSIX shell (for the smoke script); nothing else.

```bash
git clone https://github.com/JaydenCJ/barmux && cd barmux
go build ./...
go test ./...
bash scripts/smoke.sh
```

`scripts/smoke.sh` builds the binary and drives the real parent/child
loop — a named pipe, concurrent shell writers, replay, and every
documented exit code; it must finish by printing `SMOKE OK`.

## Before you open a pull request

1. `gofmt -l .` reports nothing (formatting is enforced).
2. `go vet ./...` passes with no findings.
3. `go test ./...` passes (92 deterministic tests, no network, no sleeps).
4. `bash scripts/smoke.sh` prints `SMOKE OK`.
5. Add tests for behavior changes; keep logic in pure, unit-testable
   modules (protocol, state, and render never touch the OS — only
   `internal/fifo` and the run command do).

## Ground rules

- Keep dependencies at zero; adding one needs strong justification in
  the PR. barmux never talks to the network. No telemetry.
- The wire protocol is a compatibility surface: changes to
  `docs/protocol.md` need a parser test, a formatter test, and a
  round-trip test. Unknown verbs must stay tolerated, never fatal.
- The plain (CI) output format is also a compatibility surface — people
  grep it. Treat line-shape changes as breaking.
- Code comments and doc comments are written in English.
- Determinism first: renderers are pure functions of state plus explicit
  options; time only ever enters through an injected clock.

## Reporting bugs

Include the output of `barmux version`, your OS, the exact command, and
— for rendering bugs — a recorded event stream (`BARMUX_PIPE=trace.log
your-script`, then attach `trace.log`), since any stream can be replayed
byte-for-byte with `barmux render trace.log`.

## Security

Please do not open public issues for security problems; use GitHub's
private vulnerability reporting on this repository instead.
