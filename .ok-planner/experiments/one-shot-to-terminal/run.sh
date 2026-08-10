#!/bin/bash
# Experiment: story one-shot-to-terminal.
#
# Drives a two-instance compose manifest to terminal with a single
# `rimsky compose run` invocation, in a scrubbed environment with an empty
# HOME -- so no rimsky is running before the command and none can be
# addressed. The command that starts the run is the one that finishes it:
# the transcript shows both declared instances reaching terminal, and the
# control-api port the run stood up is closed once the command returns.
#
# Requires: a rimsky CLI binary (RIMSKY_BIN). No docker.
set -u

RIMSKY_BIN=${RIMSKY_BIN:?set RIMSKY_BIN to the rimsky CLI binary}
HERE=$(cd "$(dirname "$0")" && pwd)
WORK=$(mktemp -d)
fail=0

mkdir -p "$WORK/run/home"
cp "$HERE"/template-success.yml "$HERE"/template-failure.yml "$HERE"/rimsky-compose.yml "$WORK/run/"
cd "$WORK/run" || exit 1

echo "== before: nothing stood up =="
if [ -e "$WORK/run/home/.rimsky" ]; then echo "FAIL  HOME already carries rimsky config"; fail=1
else echo "PASS  empty HOME: no configured endpoint to address"; fi

echo "== single invocation =="
start=$(date +%s)
env -i PATH=/usr/bin:/bin HOME="$WORK/run/home" TMPDIR=/tmp \
  "$RIMSKY_BIN" compose run rimsky-compose.yml >stdout.txt 2>stderr.txt
rc=$?
end=$(date +%s)
echo "exit=$rc elapsed=$((end - start))s"
grep -E '^instance |^rimsky compose run:' stderr.txt | sed 's/^/    /'

for inst in alpha beta; do
  if grep -qE "^instance one-shot-demo/$inst: (success|failure) \(nodes=" stderr.txt; then
    echo "PASS  instance $inst reached terminal inside the invocation"
  else
    echo "FAIL  instance $inst never reported terminal"; fail=1
  fi
done

if grep -q '^instance one-shot-demo/alpha: success' stderr.txt; then
  echo "PASS  alpha reported success"
else echo "FAIL  alpha outcome not success"; fail=1; fi
if grep -q '^instance one-shot-demo/beta: failure' stderr.txt; then
  echo "PASS  beta reported failure"
else echo "FAIL  beta outcome not failure"; fail=1; fi

echo "== after: nothing left to tear down =="
port=$(grep -o '"addr":"127.0.0.1:[0-9]*' stderr.txt | head -1 | sed 's/.*://')
if [ -z "$port" ]; then
  echo "FAIL  could not read the control-api port the run stood up"; fail=1
else
  echo "control-api port used by the run: $port"
  if curl -s -m 2 -o /dev/null "http://127.0.0.1:$port/v1/health"; then
    echo "FAIL  control-api on $port still answering after the command returned"; fail=1
  else
    echo "PASS  control-api on $port gone once the command returned"
  fi
fi

echo
if [ $fail -eq 0 ]; then echo "RESULT: PASS"; else echo "RESULT: FAIL"; fi
exit $fail
