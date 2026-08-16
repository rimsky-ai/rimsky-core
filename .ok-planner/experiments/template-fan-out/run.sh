#!/usr/bin/env bash
# Experiment: template-fan-out
# A fan-out node names a claim on the bundled filesystem claim producer and a
# partition request listing three partitions. A concurrency-observing HTTP
# endpoint on the host holds each work unit open long enough to see how many
# ran at once, so the run shows one work unit per sub-claim, running
# concurrently, with the parent settling only after every sub-claim resolved.
# A parallelism-1 template is the control that the observed concurrency is
# rimsky's dispatch, and a failing template shows the parent settling on the
# aggregated outcome. Public CLI against a rimsky-all-in-one container with the
# bundled filesystem claim producer configured.
set -uo pipefail

TAG="${RIMSKY_IMAGE_TAG:?set RIMSKY_IMAGE_TAG to the image tag built from this tree}"
HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../../.." && pwd)"
CLI="$ROOT/bin/rimsky"
NAME="rimsky-exp-template-fan-out"
PORT="${PORT:-$(python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()')}"
SLOW_PORT="${SLOW_PORT:-$(python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()')}"
E="http://127.0.0.1:$PORT"
WS="$(mktemp -d)"

fails=0
ok()   { echo "PASS  $*"; }
bad()  { echo "FAIL  $*"; fails=$((fails+1)); }
has()  { case "$2" in *"$1"*) ok "$3";; *) bad "$3 (missing '$1' in: $2)";; esac; }

mkdir -p "$WS/data" "$WS/templates"
for t in template-parallel template-serial template-failing; do
  sed "s/host\.docker\.internal:18999/host.docker.internal:$SLOW_PORT/" \
    "$HERE/$t.yml" > "$WS/templates/$t.yml"
done
python3 "$HERE/slow_server.py" "$SLOW_PORT" &
SLOW_PID=$!
cleanup() { kill "$SLOW_PID" >/dev/null 2>&1; docker rm -f "$NAME" >/dev/null 2>&1; rm -rf "$WS"; }
trap cleanup EXIT
until curl -sS "http://127.0.0.1:$SLOW_PORT/peak" >/dev/null 2>&1; do sleep 0.2; done

docker rm -f "$NAME" >/dev/null 2>&1
docker run -d --name "$NAME" -p "127.0.0.1:$PORT:8080" \
  -e RIMSKY_CLAIM_PRODUCER_FILESYSTEM_CONFIG=/etc/rimsky/claim-producer-filesystem.yml \
  -v "$HERE/claim-producer-filesystem.yml:/etc/rimsky/claim-producer-filesystem.yml:ro" \
  -v "$WS:/workspace:rw" \
  "rimsky-all-in-one:$TAG" >/dev/null
until "$CLI" health --endpoint "$E" >/dev/null 2>&1; do sleep 0.2; done
docker logs "$NAME" 2>&1 | grep -q 'bundled claim producer registered in-process.*claim-producer-filesystem' \
  && ok "the bundled filesystem claim producer is registered and advertises split-scope support" \
  || bad "the bundled filesystem claim producer did not register"

node_py='
import sys, json
for n in json.load(sys.stdin):
    t = n.get("node_type")
    if t:
        print("%s fresh=%d failed=%d settled=%s" % (t, n["run_summary"]["fresh_count"],
              n["run_summary"]["failed_count"], n.get("settling_signal_type")))
'
order_py='
import sys, json
raw = sys.stdin.read(); dec = json.JSONDecoder(); i = 0; evs = []
while i < len(raw):
    while i < len(raw) and raw[i] in " \n\t\r": i += 1
    if i >= len(raw): break
    o, j = dec.raw_decode(raw, i); evs.append(o); i = j
for e in sorted(evs, key=lambda x: x["id"]):
    print(e["id"], e["kind"], json.dumps(e.get("payload")))
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
events() { "$CLI" instance events "$1" --endpoint "$E" -o json 2>/dev/null | python3 -c "$order_py"; }
settle() { until state "$1" | grep -qE "^partitioned (fresh=4 |fresh=0 failed=[1-9])"; do sleep 0.2; done; }
peak()   { curl -sS "http://127.0.0.1:$SLOW_PORT/peak"; }

echo "--- one work unit per sub-claim, dispatched concurrently"
A="$(start "$WS/templates/template-parallel.yml")" || bad "the fan-out template did not register"
ok "the fan-out template registers, deploys and instantiates: $A"
settle "$A"
ev="$(events "$A")"
has '"sub_scope_descriptor_count": 3' "$ev" "the producer's split returns one sub-scope per declared partition"
has '"child_keys": ["p1", "p2", "p3"]' "$ev" "one work unit is dispatched per sub-claim, keyed by partition"
has '"num_sub_claims": 3' "$ev" "the dispatch records three sub-claims"
s="$(state "$A")"; echo "$s"
has "partitioned fresh=4 failed=0" "$s" "the parent and all three clones settle fresh"
p="$(peak)"; echo "concurrency observed: $p"
has '"peak": 3' "$p" "all three work units were in flight at the same time"
has '"served": 3' "$p" "exactly three work units ran"

echo "--- the parent settles only after every sub-claim resolves"
last_commit="$(printf '%s\n' "$ev" | grep -n 'claim_resolution.commit' | tail -1 | cut -d: -f1)"
parent_settle="$(printf '%s\n' "$ev" | grep -n 'aggregated_settlement' | tail -1 | cut -d: -f1)"
if [ -n "$last_commit" ] && [ -n "$parent_settle" ] && [ "$parent_settle" -gt "$last_commit" ]; then
  ok "the parent's aggregated settlement follows the last sub-claim's resolution"
else
  bad "could not order the parent settlement after the last sub-claim commit (commit=$last_commit settle=$parent_settle)"
fi

echo "--- control: parallelism 1 serialises the same fan-out"
curl -sS -XPOST -d '{}' "http://127.0.0.1:$SLOW_PORT/reset" >/dev/null 2>&1
kill "$SLOW_PID" >/dev/null 2>&1; wait "$SLOW_PID" 2>/dev/null
python3 "$HERE/slow_server.py" "$SLOW_PORT" &
SLOW_PID=$!
until curl -sS "http://127.0.0.1:$SLOW_PORT/peak" >/dev/null 2>&1; do sleep 0.2; done
B="$(start "$WS/templates/template-serial.yml")" || bad "the parallelism-1 template did not register"
settle "$B"
p="$(peak)"; echo "concurrency observed: $p"
has '"peak": 1' "$p" "with parallelism 1 the same three work units never overlap"
has '"served": 3' "$p" "all three work units still ran"

echo "--- the parent settles on the aggregated outcome when partitions fail"
C="$(start "$WS/templates/template-failing.yml")" || bad "the failing template did not register"
settle "$C"
s="$(state "$C")"; echo "$s"
printf '%s\n' "$s" | grep -qE '^partitioned fresh=0 failed=[1-9]' \
  && ok "no run of the fan-out settles fresh; the parent settles failed" \
  || bad "the parent did not settle failed: $s"
ev="$(events "$C")"
has "terminal/error/aggregate/strict_failed" "$ev" "the parent's settlement names the aggregation verdict over its partitions"
has "claim_resolution.abandon" "$ev" "the failed partitions' claims are abandoned rather than committed"

echo
if [ "$fails" -eq 0 ]; then echo "EXPERIMENT PASS"; else echo "EXPERIMENT FAIL ($fails)"; fi
exit "$fails"
