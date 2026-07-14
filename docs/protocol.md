# The barmux pipe protocol

Version 1 (barmux 0.1.0). Deliberately dumb: line-oriented UTF-8 text,
whitespace-separated fields, writable from any language — including plain
shell — with zero dependencies.

## Transport

The parent (`barmux run`) creates a named pipe (FIFO), exports its path
as `BARMUX_PIPE`, and holds a read+write descriptor open for the whole
run, so short-lived writers (`echo line > "$BARMUX_PIPE"` opens, writes,
closes) never cause EOF on the reader.

Rules that make concurrency safe:

- **One event per line**, terminated by `\n`.
- **Max line length: 512 bytes.** POSIX guarantees that writes up to
  `PIPE_BUF` (≥512 bytes everywhere) to a pipe are atomic, so concurrent
  writers can never tear each other's lines. Longer lines are rejected as
  malformed.
- Writers should open the pipe non-blocking (`barmux emit` does): if the
  parent has gone away, dropping a progress line always beats hanging a
  build step.
- The transport can also be a **regular file** — `BARMUX_PIPE=trace.log`
  appends events, and `barmux render trace.log` replays them later.

## Grammar

```
line    = comment | event
comment = "#" anything            ; ignored (also: blank lines)
event   = verb rest
```

The first field is the verb, the second (except for `log`) is the task
id. The **final field is always "rest of line"** and may contain spaces —
there is no quoting or escaping anywhere in the protocol.

| Verb | Form | Meaning |
|---|---|---|
| `start` | `start <id> [<total>\|-] [label…]` | announce a task; integer total makes a bar, `-` (or nothing) a spinner |
| `tick` | `tick <id> [n]` | advance progress by n (default 1) |
| `set` | `set <id> <current>` | set absolute progress |
| `total` | `total <id> <n>` | set/replace the total after the fact |
| `msg` | `msg <id> [text…]` | update the task's status message |
| `done` | `done <id> [text…]` | finish successfully (bar snaps to 100%) |
| `fail` | `fail <id> [text…]` | finish as failed, with optional reason |
| `log` | `log [text…]` | pass-through line, printed above the bars |

Task ids: 1–64 characters from `[A-Za-z0-9._:@/-]` — enough for paths
(`pkg/api`), targets (`test:unit`), and job coordinates (`shard-3`).
Counts are non-negative decimal 64-bit integers.

## Semantics

- **Implicit creation** — `tick`, `set`, `msg`, `done`, `fail` on an
  unknown id create the task (label = id, indeterminate). Partial
  instrumentation degrades softly instead of being dropped.
- **Clamping** — progress never exceeds a known total and never goes
  negative; `done` on a determinate task snaps progress to the total.
- **Restart** — `start` on a finished task reopens it from zero (a
  retried build step); `start` on a *running* task only updates its
  label/total, so multiple workers may share one bar.
- **Tolerance** — malformed lines and unknown verbs are counted and
  surfaced in the summary, never fatal (unless `render --strict`).
  Unknown verbs are the forward-compatibility path: an older parent
  ignores what a newer child says, and keeps rendering the rest.

## Wire examples

```
start build 100 Compiling objects
tick build
tick build 5
msg build cc src/main.c
start pull - Downloading layers
tick pull
log configure: using cache
done build
fail pull registry timeout
```

## Design notes

Why not JSON-per-line? Because the target writers are makefiles, shell
loops, and CI scripts where `echo "tick build"` must be enough — no
quoting rules to get wrong, nothing to install. The price is that free
text cannot contain newlines (the formatter strips control characters)
and ids have a restricted charset; both are prices worth paying for a
protocol you can speak from a Makefile one-liner.
