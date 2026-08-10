#!/bin/bash
# Experiment: story live-progress.
#
# Watches a one-shot run that holds one instance in flight while another
# settles immediately, and stamps every progress line with the wall-clock
# second it arrived at the terminal. A pass shows the operator learns each
# node's outcome while the run is still going -- the quick instance's node
# line lands seconds before the lagging instance's, and both before the run
# ends -- which is what separates "still working" from "hung".
#
# The lagging instance fetches an upstream that sleeps 8s, so a watcher who
# saw only batched-at-exit output would see nothing for the whole window.
#
# Requires: a rimsky CLI binary (RIMSKY_BIN), python3. No docker.
set -u

RIMSKY_BIN=${RIMSKY_BIN:?set RIMSKY_BIN to the rimsky CLI binary}
HERE=$(cd "$(dirname "$0")" && pwd)
SLOW_PORT=${SLOW_PORT:-18778}
SLOW_SECS=8
WORK=$(mktemp -d)
fail=0

python3 "$HERE/slow-server.py" "$SLOW_SECS" "$SLOW_PORT" &
SLOW_PID=$!
cleanup() { kill "$SLOW_PID" 2>/dev/null; }
trap cleanup EXIT
sleep 1

mkdir -p "$WORK/run/home"
cp "$HERE"/*.yml "$WORK/run/"
sed -i.bak "s|127.0.0.1:18777|127.0.0.1:$SLOW_PORT|" "$WORK/run/template-slow.yml"
cd "$WORK/run" || exit 1

t0=$(date +%s)
env -i PATH=/usr/bin:/bin HOME="$WORK/run/home" TMPDIR=/tmp \
  RIMSKY_EXECUTOR_HTTP_NODE_EGRESS_ALLOWLIST=127.0.0.0/8 \
  "$RIMSKY_BIN" compose run rimsky-compose.yml 2>&1 >/dev/null |
  while IFS= read -r line; do printf '%s %s\n' "$(date +%s)" "$line"; done >stamped.txt
tend=$(date +%s)

echo "== stamped progress lines (seconds since command start) =="
awk -v t0="$t0" '{ off = $1 - t0; $1 = ""; printf "  +%ds%s\n", off, $0 }' stamped.txt |
  grep -E 'instance live-demo|compose run:'

first_at() { # first_at <regex> -> earliest offset in seconds, or empty
  awk -v t0="$t0" -v re="$1" '$0 ~ re { print $1 - t0; exit }' stamped.txt
}
last_at() { # last_at <regex> -> latest offset in seconds, or empty
  awk -v t0="$t0" -v re="$1" '$0 ~ re { v = $1 - t0 } END { if (v != "") print v }' stamped.txt
}

quick_node=$(first_at 'instance live-demo/quick node .*: success')
quick_done=$(first_at 'instance live-demo/quick: success')
# The lagging instance's fetch node is the last of its nodes to settle; the
# earlier one is its structural root, which settles with the wake message.
lag_node=$(last_at 'instance live-demo/lagging node .*: success')
summary=$(first_at 'compose run: all-success')
total=$((tend - t0))

echo
echo "quick node terminal at +${quick_node:-?}s; quick instance terminal at +${quick_done:-?}s; lagging fetch node terminal at +${lag_node:-?}s; summary at +${summary:-?}s; command ended at +${total}s"

if [ -z "$quick_node" ]; then echo "FAIL  no per-node line for the quick instance"; fail=1
else echo "PASS  per-node lifecycle line emitted for the quick instance"; fi
if [ -z "$lag_node" ]; then echo "FAIL  no per-node line for the lagging instance"; fail=1
else echo "PASS  per-node lifecycle line emitted for the lagging instance"; fi

if [ -n "$quick_done" ] && [ -n "$lag_node" ] && [ "$quick_done" -lt "$lag_node" ]; then
  echo "PASS  quick instance settled and was reported while the lagging fetch was still in flight ($quick_done < $lag_node)"
else
  echo "FAIL  progress lines are not ordered by when the work settled"; fail=1
fi

# The lagging fetch alone takes SLOW_SECS. If the quick node's line arrived
# before that window closed, output was not batched to the end of the run.
if [ -n "$quick_node" ] && [ "$quick_node" -lt "$SLOW_SECS" ]; then
  echo "PASS  quick node's outcome was on screen while the run was still in flight (+${quick_node}s < ${SLOW_SECS}s upstream wait)"
else
  echo "FAIL  quick node's outcome did not arrive during the run"; fail=1
fi

echo
if [ $fail -eq 0 ]; then echo "RESULT: PASS"; else echo "RESULT: FAIL"; fi
exit $fail
