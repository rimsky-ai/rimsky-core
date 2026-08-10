#!/usr/bin/env bash
# Experiment: template-subscriptions
# One source node emits a terminal/success signal whose payload carries the
# executor's attribute delta. Five subscriber nodes sit on that one signal:
# exact type-path, trailing-wildcard prefix, a non-matching type-path, a CEL
# predicate the payload satisfies, and a CEL predicate it does not. The run
# shows exactly the three matching nodes fired. Public CLI against a
# rimsky-all-in-one container.
set -uo pipefail

TAG="${RIMSKY_IMAGE_TAG:?set RIMSKY_IMAGE_TAG to the image tag built from this tree}"
HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../../.." && pwd)"
CLI="$ROOT/bin/rimsky"
NAME="rimsky-exp-template-subscriptions"
PORT="${PORT:-18107}"
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
        print("%s ran=%d" % (t, n["run_summary"]["fresh_count"] + n["run_summary"]["failed_count"]))
'
ID="$("$CLI" template register "$HERE/template.yml" --endpoint "$E" -o json 2>&1 | sed -n 's/.*"template_id": "\([^"]*\)".*/\1/p')"
[ -n "$ID" ] && ok "the subscription template registers: exact, wildcard and CEL-predicated entries all admitted" \
             || bad "the subscription template did not register"
"$CLI" template deploy "$ID" --endpoint "$E" -o json >/dev/null 2>&1
IN="$("$CLI" instance create "$ID" --endpoint "$E" -o json 2>&1 | sed -n 's/.*"instance_id": "\([^"]*\)".*/\1/p')"
curl -sS -XPOST -H 'Content-Type: application/json' -H "Idempotency-Key: wake-$IN" \
  -d '{"type":""}' "$E/v1/instances/$IN/messages" >/dev/null

state() { "$CLI" instance nodes "$IN" --endpoint "$E" -o json 2>/dev/null | python3 -c "$node_py"; }
hits() { state | grep -cE '^(exact-hit|wildcard-hit|predicate-hit) ran=[1-9]'; }
until [ "$(hits)" -eq 3 ]; do sleep 0.2; done

s="$(state)"
echo "$s"
has "source ran=1" "$s" "the source node runs and emits its terminal signal"
has "exact-hit ran=1" "$s" "an exact type-path subscription fires on the matching signal"
has "wildcard-hit ran=1" "$s" "a trailing-wildcard type-path subscription fires on the matching signal"
has "predicate-hit ran=1" "$s" "a subscription whose CEL predicate the payload satisfies fires"
has "type-miss ran=0" "$s" "a subscription on a different type-path does not fire"
has "predicate-miss ran=0" "$s" "a subscription whose CEL predicate the payload fails does not fire"

echo
if [ "$fails" -eq 0 ]; then echo "EXPERIMENT PASS"; else echo "EXPERIMENT FAIL ($fails)"; fi
exit "$fails"
