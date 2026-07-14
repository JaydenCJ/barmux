#!/usr/bin/env bash
# Simulated parallel build under one barmux dashboard.
#
# Run it through barmux:
#
#   barmux run -- bash examples/parallel-build.sh
#
# Three "jobs" run concurrently and each reports its own bar by writing
# plain protocol lines to $BARMUX_PIPE with echo — no library, no SDK.
# Run the script WITHOUT barmux and it still works: $BARMUX_PIPE is
# unset, so every echo falls back to /dev/null and only the normal
# stdout lines remain.
set -euo pipefail

PIPE="${BARMUX_PIPE:-/dev/null}"

job() {
  local id="$1" total="$2" label="$3" delay="$4"
  echo "start $id $total $label" > "$PIPE"
  local i=0
  while [ "$i" -lt "$total" ]; do
    sleep "$delay" # stand-in for real work
    i=$((i + 1))
    echo "tick $id" > "$PIPE"
    echo "msg $id unit $i of $total" > "$PIPE"
  done
  echo "done $id" > "$PIPE"
}

echo "building 3 targets in parallel" # plain stdout: forwarded as a log line

job compile 24 "Compiling objects" 0.05 &
job assets 12 "Bundling assets" 0.12 &
job tests 30 "Running tests" 0.04 &
wait

echo "all jobs finished"
