#!/usr/bin/env bash
# Experiment: claim-producer-protocol
# A claim producer written for this experiment against the published
# claim-producer gRPC protocol is stood up five times on loopback, one per
# advertised write-semantics plus one that always answers Unavailable, and a
# rimsky stack is pointed at all five through the rimsky.yml claim_producers
# block. The run shows the capabilities advertisement reaching the control
# API, Open arriving with the resolved selector and the node's inert data,
# the returned claim handle reaching the executor's dispatch, and the terminal
# verbs closing each claim.
set -uo pipefail

TAG="${RIMSKY_IMAGE_TAG:?set RIMSKY_IMAGE_TAG to the image tag built from this tree}"
HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../../.." && pwd)"
CLI="$ROOT/bin/rimsky"
NAME="rimsky-exp-claim-producer-protocol"
PORT="${PORT:-18201}"
REC_PORT="${REC_PORT:-19419}"
E="http://127.0.0.1:$PORT"
WORK="$(mktemp -d)"

fails=0
ok()  { echo "PASS  $*"; }
bad() { echo "FAIL  $*"; fails=$((fails+1)); }
has() { case "$2" in *"$1"*) ok "$3";; *) bad "$3 (missing '$1')";; esac; }

PIDS=()
cleanup() {
  for p in "${PIDS[@]:-}"; do kill "$p" >/dev/null 2>&1; done
  docker rm -f "$NAME" >/dev/null 2>&1
  rm -rf "$WORK"
}
trap cleanup EXIT

go build -o "$WORK/producer" "$HERE" || { echo "build failed"; exit 1; }

start_producer() {
  if nc -z 127.0.0.1 "$1" >/dev/null 2>&1; then
    echo "port $1 is already in use; refusing to start a producer on it" >&2; exit 2
  fi
  "$WORK/producer" -grpc "127.0.0.1:$1" "${@:2}" >"$WORK/prod-$1.log" 2>&1 &
  PIDS+=("$!")
  until nc -z 127.0.0.1 "$1" >/dev/null 2>&1; do sleep 0.1; done
}

start_producer 19411 -http 127.0.0.1:19511 -name sync-producer      -semantics sync           -payload '{"region":"us-east-1"}'
start_producer 19412 -http 127.0.0.1:19512 -name staged-producer    -semantics staged_async
start_producer 19413 -http 127.0.0.1:19513 -name blocking-producer  -semantics blocking_async
start_producer 19414 -http 127.0.0.1:19514 -name readonly-producer  -semantics read_only
start_producer 19415 -http 127.0.0.1:19515 -name scarce             -semantics sync           -unavailable-class scarce/exhausted

python3 "$HERE/recorder.py" "$REC_PORT" >"$WORK/recorder.log" 2>&1 &
PIDS+=("$!")
until curl -sS "http://127.0.0.1:$REC_PORT/log" >/dev/null 2>&1; do sleep 0.1; done

docker rm -f "$NAME" >/dev/null 2>&1
docker run -d --name "$NAME" -p "127.0.0.1:$PORT:8080" \
  -e RIMSKY_EXECUTOR_HTTP_NODE_EGRESS_ALLOWLIST=0.0.0.0/0 \
  -v "$HERE/rimsky.yml:/etc/rimsky/rimsky.yml:ro" \
  "rimsky-all-in-one:$TAG" >/dev/null
until "$CLI" health --endpoint "$E" >/dev/null 2>&1; do sleep 0.2; done

register() {
  curl -sS -XPOST -H 'Content-Type: application/json' \
    -d "{\"spec\": $(cat "$1")}" "$E/v1/templates" \
    | sed -n 's/.*"template_id":"\([^"]*\)".*/\1/p'
}
start() {
  local f="$1" params="${2:-}" id in
  [ -z "$params" ] && params='{}' 
  id="$(register "$f")"
  [ -z "$id" ] && { echo "REGISTER FAILED: $f -- $(curl -sS -XPOST -H 'Content-Type: application/json' -d "{\"spec\": $(cat "$f")}" "$E/v1/templates")" >&2; return 1; }
  "$CLI" template deploy "$id" --endpoint "$E" -o json >/dev/null 2>&1
  local raw
  raw="$("$CLI" instance create "$id" --params "$params" --endpoint "$E" -o json 2>&1)"
  in="$(printf '%s' "$raw" | sed -n 's/.*"instance_id": "\([^"]*\)".*/\1/p')"
  [ -z "$in" ] && { echo "CREATE FAILED: $f -- $raw" >&2; return 1; }
  curl -sS -XPOST -H 'Content-Type: application/json' -H "Idempotency-Key: wake-$in" \
    -d '{"type":""}' "$E/v1/instances/$in/messages" >/dev/null
  printf '%s' "$in"
}
settled_count() {
  "$CLI" instance nodes "$1" --endpoint "$E" -o json 2>/dev/null | python3 -c '
import sys, json
try: rows = json.load(sys.stdin)
except Exception: print(0); raise SystemExit
n = 0
for d in rows:
    if not d.get("node_type"): continue
    s = d["run_summary"]
    if s["fresh_count"] or s["failed_count"]: n += 1
print(n)'
}
node_states() {
  "$CLI" instance nodes "$1" --endpoint "$E" -o json 2>/dev/null | python3 -c '
import sys, json
for d in json.load(sys.stdin):
    t = d.get("node_type")
    if not t: continue
    s = d["run_summary"]
    print("%s fresh=%d failed=%d signal=%s" % (t, s["fresh_count"], s["failed_count"], d.get("settling_signal_type")))'
}

echo "--- each producer's capabilities advertisement reaches the control API"
caps="$(curl -sS "$E/v1/observability/claim-producers")"
for n in sync-producer staged-producer blocking-producer readonly-producer scarce; do
  one="$(printf '%s' "$caps" | python3 -c "
import sys, json
for e in json.load(sys.stdin)['claim_producers']:
    if e['name'] == '$n': print(json.dumps(e))" )"
  echo "    $one"
  has "$n/exhausted" "$one" "the error class $n declares at startup is listed for it"
done

echo "--- one node per advertised write-semantics drives its claim to terminal"
A="$(start "$HERE/template-semantics.json" '{"region":"us-east-1"}')" || bad "the semantics template did not register"
until [ "$(settled_count "$A")" = 4 ]; do sleep 0.3; done
st="$(node_states "$A")"; echo "$st"
for t in sync-writer staged-writer blocking-writer readonly-reader; do
  printf '%s\n' "$st" | grep -q "^$t fresh=1 failed=0" \
    && ok "$t settled fresh against its producer" \
    || bad "$t did not settle fresh"
done

echo "--- Open carries the resolved selector, the intent, the alias and the node's inert data"
sl="$(curl -sS http://127.0.0.1:19511/log)"
echo "    $sl"
has '"verb":"Open"' "$sl" "the sync producer received Open"
has '"selector":"/regions/us-east-1"' "$sl" "the selector reached the producer with the instance param substituted"
has '"intent":"rw"' "$sl" "the declared intent reached the producer"
has '"alias":"held"' "$sl" "the declared alias reached the producer"
has 'lease_hint' "$sl" "the node's inert data reached the producer unread"
has '"verb":"Commit"' "$sl" "the successful write claim was committed through the producer"
rl="$(curl -sS http://127.0.0.1:19514/log)"
echo "    $rl"
has '"intent":"r"' "$rl" "the read-only producer received a read-intent Open"
has '"verb":"Commit"' "$rl" "the read claim was closed through a terminal verb"

echo "--- the claim handle the producer returned drives the executor dispatch"
rec="$(curl -sS "http://127.0.0.1:$REC_PORT/log")"
echo "    $rec"
has '"held_addr": "sync-producer://regions/us-east-1"' "$rec" "the address the sync producer returned reached its node's dispatch"
has 'selector' "$(printf '%s' "$rec" | python3 -c "
import sys, json
for e in json.load(sys.stdin):
    if e['path'].endswith('sync-writer'): print(e['body'].get('held_scope'))")" "the claim scope the producer returned reached its node's dispatch"
has '"held_region": "us-east-1"' "$rec" "a field of the payload the producer returned reached its node's dispatch"
has '"held_addr": "staged-producer://staged-area"' "$rec" "the staged producer's address reached its node's dispatch"
has '"held_addr": "blocking-producer://blocking-area"' "$rec" "the blocking producer's address reached its node's dispatch"
has '"held_addr": "readonly-producer://catalog"' "$rec" "the read-only producer's address reached its node's dispatch"

echo "--- the persisted claim handles carry each producer's realized write semantics"
ch="$(curl -sS "$E/v1/observability/claim-handles?limit=100")"
for pair in 'sync-producer:sync' 'staged-producer:staged_async' 'blocking-producer:blocking_async' 'readonly-producer:read_only'; do
  n="${pair%%:*}"; s="${pair##*:}"
  row="$(printf '%s' "$ch" | python3 -c "
import sys, json
for h in json.load(sys.stdin)['claim_handles']:
    if h.get('producer_name') == '$n': print(json.dumps(h)); break")"
  echo "    $row"
  has "\"realized_write_semantics\": \"$s\"" "$row" "$n's claim handle records realized write semantics $s"
done

echo "--- a producer that answers Unavailable settles the node on its own declared error class"
B="$(start "$HERE/template-unavailable.json")" || bad "the unavailable template did not register"
until [ "$(settled_count "$B")" = 1 ]; do sleep 0.3; done
st="$(node_states "$B")"; echo "$st"
has "signal=terminal/error/scarce/exhausted" "$st" "the node settles on the error class the producer declared and returned"
ul="$(curl -sS http://127.0.0.1:19515/log)"
has '"result":"unavailable"' "$ul" "the producer answered Open with Unavailable"

echo
if [ "$fails" -eq 0 ]; then echo "EXPERIMENT PASS"; else echo "EXPERIMENT FAIL ($fails)"; fi
exit "$fails"
