#!/usr/bin/env bash
# Experiment: template-sub-graph-delegation
# Registers a template whose main-graph node delegates to a named sub-graph,
# runs an instance, and reads the event log to show the calling node dispatched
# the sub-graph as its execution unit, that the sub-graph's exit carried its
# outcome back, and that the caller settled only after that carry. A second
# template whose sub-graph exit fails shows the caller settles failed on the
# sub-graph's outcome. Public CLI against a rimsky-all-in-one container.
set -uo pipefail

TAG="${RIMSKY_IMAGE_TAG:?set RIMSKY_IMAGE_TAG to the image tag built from this tree}"
HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../../.." && pwd)"
CLI="$ROOT/bin/rimsky"
NAME="rimsky-exp-template-sub-graph-delegation"
PORT="${PORT:-18106}"
E="http://127.0.0.1:$PORT"

fails=0
ok()   { echo "PASS  $*"; }
bad()  { echo "FAIL  $*"; fails=$((fails+1)); }
has()  { case "$2" in *"$1"*) ok "$3";; *) bad "$3 (missing '$1' in: $2)";; esac; }

docker rm -f "$NAME" >/dev/null 2>&1
docker run -d --name "$NAME" -p "127.0.0.1:$PORT:8080" "rimsky-all-in-one:$TAG" >/dev/null
trap 'docker rm -f "$NAME" >/dev/null 2>&1' EXIT
until "$CLI" health --endpoint "$E" >/dev/null 2>&1; do sleep 0.2; done

order_py='
import sys, json
raw = sys.stdin.read(); dec = json.JSONDecoder(); i = 0; evs = []
while i < len(raw):
    while i < len(raw) and raw[i] in " \n\t\r": i += 1
    if i >= len(raw): break
    o, j = dec.raw_decode(raw, i); evs.append(o); i = j
for e in sorted(evs, key=lambda x: x["id"]):
    print(e["id"], e["kind"], (e.get("node_id") or "")[:8])
'
node_py='
import sys, json
for n in json.load(sys.stdin):
    print(repr(n.get("node_type")), n["run_summary"]["fresh_count"], n["run_summary"]["failed_count"], n.get("settling_signal_type"))
'

run_template() {
  local f="$1" id in
  id="$("$CLI" template register "$f" --endpoint "$E" -o json 2>&1 | sed -n 's/.*"template_id": "\([^"]*\)".*/\1/p')"
  [ -z "$id" ] && { echo "REGISTER FAILED: $f" >&2; return 1; }
  "$CLI" template deploy "$id" --endpoint "$E" -o json >/dev/null 2>&1
  in="$("$CLI" instance create "$id" --endpoint "$E" -o json 2>&1 | sed -n 's/.*"instance_id": "\([^"]*\)".*/\1/p')"
  curl -sS -XPOST -H 'Content-Type: application/json' -H "Idempotency-Key: wake-$in" \
    -d '{"type":""}' "$E/v1/instances/$in/messages" >/dev/null
  printf '%s' "$in"
}
settled() { "$CLI" instance nodes "$1" --endpoint "$E" -o json 2>/dev/null | python3 -c "$node_py" | grep -cE "^'caller' (1 0|0 1) terminal"; }

echo "--- a node that delegates to a named sub-graph"
A="$(run_template "$HERE/template.yml")" || bad "the delegating template did not register"
ok "the delegating template registers, deploys and instantiates: $A"
until [ "$(settled "$A")" -gt 0 ]; do sleep 0.2; done

ev="$("$CLI" instance events "$A" --endpoint "$E" -o json 2>&1 | python3 -c "$order_py")"
has "subgraph.dispatched" "$ev" "the calling node dispatches the sub-graph as its execution unit"
has "subgraph.exit_carry" "$ev" "the sub-graph's exit carries its outcome back to the caller"

nodes="$("$CLI" instance nodes "$A" --endpoint "$E" -o json 2>&1 | python3 -c "$node_py")"
echo "$nodes"
has "'caller' 1 0 terminal/success" "$nodes" "the calling node settles successfully"
has "'inner-entry' 0 0 None" "$nodes" "the sub-graph entry runs inside the caller rather than as its own run"
has "'inner-mid' 1 0 terminal/success" "$nodes" "the sub-graph's internal node runs"
has "'inner-exit' 1 0 terminal/success" "$nodes" "the sub-graph's exit runs"

caller="$(printf '%s\n' "$ev" | grep 'subgraph.dispatched' | awk '{print $3}')"
carry_id="$(printf '%s\n' "$ev" | grep 'subgraph.exit_carry' | awk '{print $1}')"
settle_id="$(printf '%s\n' "$ev" | awk -v c="$caller" '$2 ~ /^terminal\// && $3 == c {print $1}' | tail -1)"
if [ -n "$carry_id" ] && [ -n "$settle_id" ] && [ "$settle_id" -gt "$carry_id" ]; then
  ok "the caller's settling signal (event $settle_id) follows the sub-graph's exit carry (event $carry_id)"
else
  bad "could not order the caller's settle after the exit carry (carry=$carry_id settle=$settle_id)"
fi

echo "--- the caller settles on the sub-graph's outcome, failure included"
B="$(run_template "$HERE/template-failing-exit.yml")" || bad "the failing-exit template did not register"
until [ "$(settled "$B")" -gt 0 ]; do sleep 0.2; done
nodes="$("$CLI" instance nodes "$B" --endpoint "$E" -o json 2>&1 | python3 -c "$node_py")"
echo "$nodes"
has "'inner-exit' 0 1 terminal/error/verifier/check_failed" "$nodes" "the sub-graph's exit fails"
has "'caller' 0 1 terminal/error/aggregate" "$nodes" "the calling node settles failed, carrying the sub-graph's outcome"

echo
if [ "$fails" -eq 0 ]; then echo "EXPERIMENT PASS"; else echo "EXPERIMENT FAIL ($fails)"; fi
exit "$fails"
