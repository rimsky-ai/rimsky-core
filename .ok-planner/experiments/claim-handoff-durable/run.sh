#!/usr/bin/env bash
# Experiment: claim-handoff-durable
# An acquirer declares a durable claim and a downstream node co-holds it by
# alias. A third node is woken only by a later message, so it dispatches in a
# frame of its own without re-dispatching the acquirer. The run shows the
# claim-handle row surviving the dispatch as one committed durable row, the
# later dispatch co-holding that same row by alias, a competing instance
# refused while it is held, and what each operator action does to the claim.
set -uo pipefail

TAG="${RIMSKY_IMAGE_TAG:?set RIMSKY_IMAGE_TAG to the image tag built from this tree}"
HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../../.." && pwd)"
CLI="$ROOT/bin/rimsky"
NAME="rimsky-exp-claim-handoff-durable"
PORT="${PORT:-18207}"
REC_PORT="${REC_PORT:-19479}"
CP_GRPC="${CP_GRPC:-19481}"
CP_HTTP="${CP_HTTP:-19581}"
E="http://127.0.0.1:$PORT"
WORK="$(mktemp -d)"

fails=0
ok()  { echo "PASS  $*"; }
bad() { echo "FAIL  $*"; fails=$((fails+1)); }
has() { if printf '%s' "$2" | grep -qF -- "$1"; then ok "$3"; else bad "$3 (missing '$1')"; fi; }

PIDS=()
cleanup() {
  for p in "${PIDS[@]:-}"; do kill "$p" >/dev/null 2>&1; done
  docker rm -f "$NAME" >/dev/null 2>&1
  rm -rf "$WORK"
}
trap cleanup EXIT

for p in "$REC_PORT" "$CP_GRPC" "$CP_HTTP" "$PORT"; do
  if nc -z 127.0.0.1 "$p" >/dev/null 2>&1; then echo "port $p already in use" >&2; exit 2; fi
done

go build -o "$WORK/producer" "$HERE" || { echo "build failed"; exit 1; }
"$WORK/producer" -grpc "127.0.0.1:$CP_GRPC" -http "127.0.0.1:$CP_HTTP" \
  -name asset-store -semantics sync >"$WORK/producer.log" 2>&1 &
PIDS+=("$!")
until nc -z 127.0.0.1 "$CP_GRPC" >/dev/null 2>&1; do sleep 0.1; done

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
send() {
  curl -sS -XPOST -H 'Content-Type: application/json' -H "Idempotency-Key: msg-$1-$3" \
    -d "{\"type\":\"$2\"}" "$E/v1/instances/$1/messages" >/dev/null
}
start() {
  local f="$1" params="$2" id in
  id="$(register "$f")"
  [ -z "$id" ] && { echo "REGISTER FAILED: $f -- $(curl -sS -XPOST -H 'Content-Type: application/json' -d "{\"spec\": $(cat "$f")}" "$E/v1/templates")" >&2; return 1; }
  "$CLI" template deploy "$id" --endpoint "$E" -o json >/dev/null 2>&1
  in="$("$CLI" instance create "$id" --params "$params" --endpoint "$E" -o json 2>&1 | sed -n 's/.*"instance_id": "\([^"]*\)".*/\1/p')"
  [ -z "$in" ] && { echo "CREATE FAILED: $f" >&2; return 1; }
  send "$in" "" wake1
  printf '%s' "$in"
}
node_states() {
  "$CLI" instance nodes "$1" --endpoint "$E" -o json 2>/dev/null | python3 -c '
import sys, json
try: rows = json.load(sys.stdin)
except Exception: raise SystemExit
for d in rows:
    t = d.get("node_type")
    if not t: continue
    s = d["run_summary"]
    print("%s fresh=%d failed=%d signal=%s" % (t, s["fresh_count"], s["failed_count"], d.get("settling_signal_type")))'
}
settle() {
  until node_states "$1" | grep -qE "^$2 .*(fresh=[1-9]|failed=[1-9])"; do sleep 0.3; done
  node_states "$1" | grep -E "^$2 "
}
rows_for() {
  curl -sS "$E/v1/observability/claim-handles?limit=200&instance_id=$1" | python3 -c "
import sys, json
print(json.dumps([h for h in (json.load(sys.stdin).get('claim_handles') or [])
                  if json.dumps(h.get('claim_scope_data')).find('$2') >= 0]))"
}
count_rows() { printf '%s' "$(rows_for "$1" "$2")" | python3 -c 'import sys,json; print(len(json.load(sys.stdin)))'; }
verbs_for() {
  curl -sS "http://127.0.0.1:$CP_HTTP/log" | python3 -c "
import sys, json
for c in json.load(sys.stdin):
    if c.get('selector') == '$1': print(c['verb'])"
}
posts_for() {
  curl -sS "http://127.0.0.1:$REC_PORT/log" | python3 -c "
import sys, json
print(sum(1 for e in json.load(sys.stdin) if e['path'].endswith('/$1')))"
}
last_addr() {
  curl -sS "http://127.0.0.1:$REC_PORT/log" | python3 -c "
import sys, json
out = [e['body'].get('held_addr') for e in json.load(sys.stdin) if e['path'].endswith('/$1')]
print(out[-1] if out else '')"
}
holders_of() {
  curl -sS "$E/v1/claim-handles/$1/holders" | python3 -c "
import sys, json
d = json.load(sys.stdin)
print(len(d.get('holders') or d.get('claim_holders') or []))"
}

echo "--- the acquirer's durable claim is co-held in its own dispatch and survives it"
A="$(start "$HERE/template-durable.json" '{"selector":"/asset/one"}')" || bad "durable template did not register"
settle "$A" acquirer >/dev/null
st="$(settle "$A" co-holder)"; echo "    $(node_states "$A" | tr '\n' ' ')"
has "co-holder fresh=1 failed=0" "$st" "the co-holder settled fresh on the acquirer's claim"
h="$(rows_for "$A" /asset/one)"; echo "    $h"
has '"lifetime": "durable"' "$h" "the claim handle records the declared durable lifetime"
has '"state": "committed"' "$h" "the claim handle is promoted to committed rather than reaped"
[ "$(count_rows "$A" /asset/one)" = 1 ] \
  && ok "the instance holds exactly one claim-handle row for that scope" \
  || bad "the instance holds $(count_rows "$A" /asset/one) rows for that scope"
row_id="$(printf '%s' "$h" | python3 -c 'import sys,json; print(json.load(sys.stdin)[0]["id"])')"
addr1="$(last_addr co-holder)"
ok "the co-holder's claim address is $addr1"

echo "--- while it is held, another instance cannot take the same scope"
B="$(start "$HERE/template-competitor.json" '{"selector":"/asset/one"}')" || bad "competitor template did not register"
st="$(settle "$B" competitor)"; echo "    $st"
has "signal=terminal/error/acquire/unavailable" "$st" "the competing instance was refused: the producer still occupies the scope"

echo "--- a later dispatch of the same instance co-holds that same durable row by alias"
before_holders="$(holders_of "$row_id")"
before_runs="$(posts_for co-holder)"
send "$A" wake/later wake2
until [ "$(posts_for co-holder)" -gt "$before_runs" ]; do sleep 0.3; done
until [ "$(node_states "$A" | grep -c '^co-holder fresh=2')" = 1 ]; do sleep 0.3; done
st="$(node_states "$A" | grep '^co-holder ')"; echo "    $st"
has "co-holder fresh=2 failed=0" "$st" "the message woke the co-holder into a second dispatch of its own"
[ "$(last_addr co-holder)" = "$addr1" ] \
  && ok "the later dispatch read the same claim address by alias" \
  || bad "the later dispatch read $(last_addr co-holder), not $addr1"
h2="$(rows_for "$A" /asset/one)"
has "\"id\": \"$row_id\"" "$h2" "the first dispatch's durable claim-handle row is still there"
echo "    durable rows for that scope after the later dispatch: $(count_rows "$A" /asset/one)"
[ "$(count_rows "$A" /asset/one)" = 1 ] \
  && ok "the later dispatch co-held the same row rather than opening another" \
  || bad "the later dispatch left $(count_rows "$A" /asset/one) rows for that scope"
after_holders="$(holders_of "$row_id")"
echo "    holders on that one row: $before_holders then $after_holders"
[ "$after_holders" -gt "$before_holders" ] \
  && ok "the later dispatch is registered as a holder on the first dispatch's row" \
  || bad "the later dispatch added no holder to the first dispatch's row"
v="$(verbs_for /asset/one)"; echo "    verbs: $(printf '%s' "$v" | tr '\n' ' ')"
[ "$(printf '%s\n' "$v" | grep -c '^Open$')" = 1 ] \
  && ok "the producer was asked to open the scope exactly once across both dispatches" \
  || bad "the producer received $(printf '%s\n' "$v" | grep -c '^Open$') Opens across the two dispatches"
[ "$(printf '%s\n' "$v" | grep -c '^Release$')" = 0 ] \
  && ok "nothing so far has asked the producer to release the claim" \
  || bad "the producer was asked to release the claim without an operator action"

echo "--- terminating the instance"
"$CLI" instance kill "$A" --force --endpoint "$E" -o json >/dev/null 2>&1
until "$CLI" instance get "$A" --endpoint "$E" -o json 2>/dev/null | grep -q terminated_at; do sleep 0.3; done
ok "the instance reports terminated"
until [ "$("$CLI" instance nodes "$A" --endpoint "$E" -o json 2>/dev/null | grep -c '"active_count": 0')" -ge 1 ]; do sleep 0.3; done
echo "    verbs after termination: $(printf '%s' "$(verbs_for /asset/one)" | tr '\n' ' ')"
echo "    durable rows after termination: $(count_rows "$A" /asset/one)"
echo "    rows after termination: $(rows_for "$A" /asset/one)"
C="$(start "$HERE/template-competitor.json" '{"selector":"/asset/one"}')" || bad "competitor template did not register"
st="$(settle "$C" competitor)"; echo "    competitor after termination: $st"
case "$st" in
  *"terminal/error/acquire/unavailable"*) echo "    OBSERVED: termination alone did not release the claim" ;;
  *) echo "    OBSERVED: the scope was claimable after termination" ;;
esac

echo "--- deleting the instance is the operator action that releases it"
del="$("$CLI" instance delete "$A" --endpoint "$E" -o json 2>&1)"; echo "    delete: $del"
for _ in $(seq 1 450); do [ "$(printf '%s\n' "$(verbs_for /asset/one)" | grep -c '^Release$')" -ge 1 ] && break; sleep 0.2; done
echo "    verbs after delete: $(printf '%s' "$(verbs_for /asset/one)" | tr '\n' ' ')"
echo "    producer-verb outbox: $(curl -sS "$E/v1/admin/diagnostics/producer-outbox")"
echo "    rows after delete: $(rows_for "$A" /asset/one)"
echo "    stack log lines naming the outbox or a release:"
docker logs "$NAME" 2>&1 | grep -iE 'outbox|release|durable' | tail -20 | sed 's/^/      /'
[ "$(printf '%s\n' "$(verbs_for /asset/one)" | grep -c '^Release$')" -ge 1 ] \
  && ok "deleting the instance released the durable claim through the producer" \
  || bad "deleting the instance sent the producer no Release"
D="$(start "$HERE/template-competitor.json" '{"selector":"/asset/one"}')" || bad "competitor template did not register"
st="$(settle "$D" competitor)"; echo "    $st"
has "competitor fresh=1 failed=0" "$st" "the scope another instance was refused is claimable once the holding instance is deleted"

echo
if [ "$fails" -eq 0 ]; then echo "EXPERIMENT PASS"; else echo "EXPERIMENT FAIL ($fails)"; fi
exit "$fails"
