#!/usr/bin/env bash
# Experiment: template-error-policy
# Four templates declare the same node against the same deterministic executor
# failure (a shape check that cannot pass), differing only in the routing action
# declared for that error class. Each run shows the runtime honouring that
# action: pass settles the run fresh, give_up settles it failed, retry re-runs
# under the declared cap and then gives up, release_and_requeue releases the run
# and re-queues it for a fresh attempt. Public CLI against a rimsky-all-in-one
# container.
set -uo pipefail

TAG="${RIMSKY_IMAGE_TAG:?set RIMSKY_IMAGE_TAG to the image tag built from this tree}"
HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../../.." && pwd)"
CLI="$ROOT/bin/rimsky"
NAME="rimsky-exp-template-error-policy"
PORT="${PORT:-18108}"
E="http://127.0.0.1:$PORT"

fails=0
ok()   { echo "PASS  $*"; }
bad()  { echo "FAIL  $*"; fails=$((fails+1)); }
has()  { case "$2" in *"$1"*) ok "$3";; *) bad "$3 (missing '$1' in: $2)";; esac; }

docker rm -f "$NAME" >/dev/null 2>&1
docker run -d --name "$NAME" -p "127.0.0.1:$PORT:8080" "rimsky-all-in-one:$TAG" >/dev/null
trap 'docker rm -f "$NAME" >/dev/null 2>&1' EXIT
until "$CLI" health --endpoint "$E" >/dev/null 2>&1; do sleep 0.2; done

node_py='
import sys, json
for n in json.load(sys.stdin):
    t = n.get("node_type")
    if t:
        print("%s fresh=%d failed=%d settled=%s" % (t, n["run_summary"]["fresh_count"],
              n["run_summary"]["failed_count"], n.get("settling_signal_type")))
'
start() {
  local f="$1" id in
  id="$("$CLI" template register "$f" --endpoint "$E" -o json 2>&1 | sed -n 's/.*"template_id": "\([^"]*\)".*/\1/p')"
  [ -z "$id" ] && { echo "REGISTER FAILED: $f" >&2; return 1; }
  "$CLI" template deploy "$id" --endpoint "$E" -o json >/dev/null 2>&1
  in="$("$CLI" instance create "$id" --endpoint "$E" -o json 2>&1 | sed -n 's/.*"instance_id": "\([^"]*\)".*/\1/p')"
  curl -sS -XPOST -H 'Content-Type: application/json' -H "Idempotency-Key: wake-$in" \
    -d '{"type":""}' "$E/v1/instances/$in/messages" >/dev/null
  printf '%s' "$in"
}
state()  { "$CLI" instance nodes "$1" --endpoint "$E" -o json 2>/dev/null | python3 -c "$node_py"; }
kinds()  { "$CLI" instance events "$1" --endpoint "$E" -o json 2>/dev/null | grep '"kind"'; }
settle() { until state "$1" | grep -qE "^failing (fresh=1|fresh=0 failed=1)"; do sleep 0.2; done; }

echo "--- action: pass"
A="$(start "$HERE/template-pass.yml")" || bad "the pass template did not register"
settle "$A"
s="$(state "$A")"; echo "$s"
has "failing fresh=1 failed=0" "$s" "a class routed to pass settles the run fresh despite the executor error"
has "settled=terminal/error/verifier/check_failed/row_count_absolute" "$s" "the settling signal still names the error class that was passed"

echo "--- action: give_up"
B="$(start "$HERE/template-give_up.yml")" || bad "the give_up template did not register"
settle "$B"
s="$(state "$B")"; echo "$s"
has "failing fresh=0 failed=1" "$s" "a class routed to give_up settles the run failed"

echo "--- action: retry, under the declared cap"
C="$(start "$HERE/template-retry.yml")" || bad "the retry template did not register"
settle "$C"
k="$(kinds "$C")"
has "transient/retry/1/verifier/check_failed/row_count_absolute" "$k" "the first retry is taken and signalled"
has "transient/retry/2/verifier/check_failed/row_count_absolute" "$k" "the second retry is taken and signalled"
case "$k" in *"transient/retry/3/"*) bad "a third retry ran past the declared cap of 2";; *) ok "no retry runs past the declared cap of 2";; esac
s="$(state "$C")"; echo "$s"
has "failing fresh=0 failed=1" "$s" "the run settles failed once the retry budget is spent"

echo "--- action: release_and_requeue"
D="$(start "$HERE/template-release_and_requeue.yml")" || bad "the release_and_requeue template did not register"
until [ "$(kinds "$D" | grep -c 'transient/release_and_requeue/verifier/check_failed/row_count_absolute')" -ge 2 ]; do sleep 0.2; done
ok "each failure emits a release-and-requeue signal and the run is re-queued for a fresh attempt"
n="$(kinds "$D" | grep -c work_started)"
[ "$n" -ge 2 ] && ok "the re-queued run is dispatched again ($n dispatches observed)" || bad "the run was not re-dispatched"
s="$(state "$D")"; echo "$s"
has "failing fresh=0 failed=0" "$s" "a released-and-requeued run neither passes nor fails: it goes back for another attempt"
"$CLI" instance kill "$D" --force --endpoint "$E" -o json >/dev/null 2>&1

echo
if [ "$fails" -eq 0 ]; then echo "EXPERIMENT PASS"; else echo "EXPERIMENT FAIL ($fails)"; fi
exit "$fails"
