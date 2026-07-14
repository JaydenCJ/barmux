#!/usr/bin/env bash
# End-to-end smoke test for barmux: builds the binary, then exercises the
# full parent/child loop — a real named pipe, concurrent shell writers,
# render replay, emit canonicalization, and every documented exit code.
# No network, idempotent, finishes in seconds.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT

fail() {
  echo "SMOKE FAIL: $*" >&2
  exit 1
}

BIN="$WORKDIR/barmux"

echo "1. build"
(cd "$ROOT" && go build -o "$BIN" ./cmd/barmux) || fail "go build failed"

echo "2. version matches manifest"
"$BIN" --version | grep -qx "barmux 0.1.0" || fail "--version mismatch"

echo "3. run: children write the pipe protocol, parent renders CI fallback"
OUT="$("$BIN" run -- sh -c '
  echo "start build 4 Compiling objects" > "$BARMUX_PIPE"
  echo "tick build 2" > "$BARMUX_PIPE"
  echo "msg build cc main.c" > "$BARMUX_PIPE"
  echo "tick build 2" > "$BARMUX_PIPE"
  echo "done build" > "$BARMUX_PIPE"
')"
echo "$OUT" | grep -q "\[build\] start: Compiling objects (0/4)" || fail "start line missing"
echo "$OUT" | grep -q "\[build\]  50% (2/4)" || fail "50% milestone missing"
echo "$OUT" | grep -q "\[build\] done (4/4)" || fail "done line missing"
echo "$OUT" | grep -q "1 task · 1 done · overall 100%" || fail "summary missing"

echo "4. run: concurrent writers survive intact (PIPE_BUF atomicity)"
OUT="$("$BIN" run -- sh -c '
  for j in api web docs cli; do
    (
      echo "start $j 20 Job $j" > "$BARMUX_PIPE"
      i=0
      while [ $i -lt 20 ]; do echo "tick $j" > "$BARMUX_PIPE"; i=$((i+1)); done
      echo "done $j" > "$BARMUX_PIPE"
    ) &
  done
  wait
')"
for j in api web docs cli; do
  echo "$OUT" | grep -q "\[$j\] done (20/20)" || fail "concurrent job $j lost events"
done
echo "$OUT" | grep -q "4 tasks · 4 done · overall 100%" || fail "concurrent summary wrong"

echo "5. run: failed task exits 1, child exit code propagates"
if "$BIN" run -- sh -c 'echo "fail deploy bad credentials" > "$BARMUX_PIPE"' >/dev/null; then
  fail "failed task should exit 1"
fi
set +e
"$BIN" run -- sh -c 'exit 7' >/dev/null 2>&1
[ $? -eq 7 ] || fail "child exit code not propagated"
set -e

echo "6. emit canonicalizes and appends to a trace file"
TRACE="$WORKDIR/trace.log"
BARMUX_PIPE="$TRACE" "$BIN" emit start build 3 Compiling objects || fail "emit start"
BARMUX_PIPE="$TRACE" "$BIN" emit tick build || fail "emit tick"
BARMUX_PIPE="$TRACE" "$BIN" emit tick build 2 || fail "emit tick 2"
BARMUX_PIPE="$TRACE" "$BIN" emit done build all green || fail "emit done"
diff <(printf 'start build 3 Compiling objects\ntick build 1\ntick build 2\ndone build all green\n') "$TRACE" \
  || fail "trace file is not canonical"

echo "7. emit degrades silently with no dashboard listening"
env -u BARMUX_PIPE "$BIN" emit tick build || fail "emit without pipe must succeed"
if env -u BARMUX_PIPE "$BIN" emit --check tick build >/dev/null; then
  fail "emit --check should exit 1 with no listener"
fi

echo "8. render replays the recorded trace offline"
OUT="$("$BIN" render "$TRACE")"
echo "$OUT" | grep -q "\[build\] start: Compiling objects (0/3)" || fail "replay start missing"
echo "$OUT" | grep -q "\[build\] done (3/3)" || fail "replay done missing"

echo "9. render --frame prints a final dashboard snapshot"
"$BIN" render --frame --width 100 "$TRACE" | grep -q "100% (3/3)" || fail "frame snapshot wrong"

echo "10. malformed input: tolerated by default, fatal under --strict"
printf 'garbage !!\nstart b 1\ntick b\ndone b\n' > "$WORKDIR/dirty.log"
"$BIN" render "$WORKDIR/dirty.log" | grep -q "1 malformed line" || fail "malformed not counted"
set +e
"$BIN" render --strict "$WORKDIR/dirty.log" >/dev/null 2>&1
[ $? -eq 3 ] || fail "--strict should exit 3"
set -e

echo "11. usage errors exit 2"
set +e
"$BIN" frobnicate >/dev/null 2>&1
[ $? -eq 2 ] || fail "unknown command should exit 2"
"$BIN" emit warp core >/dev/null 2>&1
[ $? -eq 2 ] || fail "unknown verb should exit 2"
set -e

echo "SMOKE OK"
