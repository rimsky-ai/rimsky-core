#!/bin/bash
# Experiment: story local-orchestrator-zero-config.
#
# Runs an ad-hoc template with one binary and one command, in an environment
# scrubbed of every rimsky variable and given an empty HOME (so no ~/.rimsky
# config and no endpoint resolve). No docker, no compose stack, no external
# executor service. The template's node targets the bundled
# verifier-shape-checks executor, which the self-hosted stack registers
# in-process; the pass/fail pair proves the real service ran its own check
# logic rather than a stub.
#
# Requires: a rimsky CLI binary (RIMSKY_BIN).
set -u

RIMSKY_BIN=${RIMSKY_BIN:?set RIMSKY_BIN to the rimsky CLI binary}
HERE=$(cd "$(dirname "$0")" && pwd)
WORK=$(mktemp -d)
fail=0

run_case() { # run_case <template> <expected-exit> <expected-line-fragment>
  local tpl=$1 want=$2 frag=$3
  local dir="$WORK/$(basename "$tpl" .yml)"
  mkdir -p "$dir/home"
  cp "$HERE/$tpl" "$dir/"
  ( cd "$dir" && env -i PATH=/usr/bin:/bin HOME="$dir/home" TMPDIR=/tmp \
      "$RIMSKY_BIN" run "$tpl" >stdout.txt 2>stderr.txt )
  local rc=$?
  echo "--- $tpl: exit $rc"
  grep -E '^instance |^rimsky run:' "$dir/stderr.txt" | sed 's/^/    /'
  if [ "$rc" != "$want" ]; then echo "FAIL  $tpl: exit $rc, want $want"; fail=1
  else echo "PASS  $tpl: exit $want"; fi
  if grep -qF "$frag" "$dir/stderr.txt"; then echo "PASS  $tpl: saw [$frag]"
  else echo "FAIL  $tpl: expected [$frag] in transcript"; fail=1; fi
  if grep -qF 'bundled executor registered in-process' "$dir/stderr.txt" &&
     grep -qF 'verifier-shape-checks' "$dir/stderr.txt"; then
    echo "PASS  $tpl: bundled verifier-shape-checks registered in-process"
  else
    echo "FAIL  $tpl: bundled executor was not registered in-process"; fail=1
  fi
  if grep -qF 'RIMSKY_CONFIG' "$dir/stderr.txt" && grep -qF 'no such file' "$dir/stderr.txt"; then
    echo "FAIL  $tpl: config file was expected"; fail=1
  fi
}

# Clean rows: the no_nulls check passes, the node settles success, exit 0.
run_case template.yml 0 'terminal/success'
# One null id: the same bundled check fails the node, exit 1 with the
# verifier's own error class.
run_case template-violating-rows.yml 1 'verifier/check_failed/no_nulls'

echo
if [ $fail -eq 0 ]; then echo "RESULT: PASS"; else echo "RESULT: FAIL"; fi
exit $fail
