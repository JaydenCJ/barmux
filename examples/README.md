# barmux examples

Two runnable examples, both offline and self-contained. Build the binary
first (`go build -o barmux ./cmd/barmux`) and put it on your `PATH` or
call it with an explicit path.

## parallel-build.sh

Three concurrent shell jobs, each driving its own bar with nothing but
`echo` into `$BARMUX_PIPE`:

```bash
barmux run -- bash examples/parallel-build.sh
```

On a terminal you get three live bars plus a summary; when piped or in
CI (`barmux run ... | cat`) the same run produces clean milestone lines.
Run the script *without* barmux and it degrades to plain output —
`$BARMUX_PIPE` falls back to `/dev/null`.

## Makefile

A `make -j` integration using the `barmux emit` helper, which validates
and canonicalizes every line and silently no-ops when no dashboard is
listening:

```bash
barmux run -- make -j3 -f examples/Makefile all
```

Three targets (`compile`, `docs`, `package`) report in parallel; the
`package` target shows an indeterminate spinner task.

Both examples use short sleeps only to make the live animation visible;
the protocol itself has no timing requirements.
