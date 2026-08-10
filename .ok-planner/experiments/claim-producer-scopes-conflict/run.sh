#!/usr/bin/env bash
# Experiment: claim-producer-scopes-conflict
# A claim producer written for this experiment advertises the scopes-conflict
# capability and defines overlap as "the two selectors end in the same path
# segment", so scopes that are byte-unequal can still overlap. A rimsky stack
# is pointed at it. One instance takes and keeps a durable claim; a second
# instance then asks for a byte-unequal but overlapping scope, and a third for
# a byte-unequal non-overlapping one. A fan-out then asks for sub-claims, one
# of which overlaps the held claim under the producer's own rule.
set -uo pipefail

TAG="${RIMSKY_IMAGE_TAG:?set RIMSKY_IMAGE_TAG to the image tag built from this tree}"
HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../../.." && pwd)"
CLI="$ROOT/bin/rimsky"
NAME="rimsky-exp-scopes-conflict"
PORT="${PORT:-18205}"
REC_PORT="${REC_PORT:-19459}"
CP_GRPC="${CP_GRPC:-19461}"
CP_HTTP="${CP_HTTP:-19561}"
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
  -name overlap-store -semantics sync -scopes-conflict -split-scope >"$WORK/producer.log" 2>&1 &
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
start() {
  local f="$1" params="$2" id in
  id="$(register "$f")"
  [ -z "$id" ] && { echo "REGISTER FAILED: $f -- $(curl -sS -XPOST -H 'Content-Type: application/json' -d "{\"spec\": $(cat "$f")}" "$E/v1/templates")" >&2; return 1; }
  "$CLI" template deploy "$id" --endpoint "$E" -o json >/dev/null 2>&1
  in="$("$CLI" instance create "$id" --params "$params" --endpoint "$E" -o json 2>&1 | sed -n 's/.*"instance_id": "\([^"]*\)".*/\1/p')"
  [ -z "$in" ] && { echo "CREATE FAILED: $f" >&2; return 1; }
  curl -sS -XPOST -H 'Content-Type: application/json' -H "Idempotency-Key: wake-$in" \
    -d '{"type":""}' "$E/v1/instances/$in/messages" >/dev/null
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
handles() { curl -sS "$E/v1/observability/claim-handles?limit=200"; }
plog()    { curl -sS "http://127.0.0.1:$CP_HTTP/log"; }

echo "--- the producer advertises the scopes-conflict capability"
has '"verb":"Capabilities"' "$(plog)" "the stack performed the capabilities handshake"

echo "--- one instance takes and keeps a claim on /west/reports"
A="$(start "$HERE/template-owner.json" '{"selector":"/west/reports"}')" || bad "owner template did not register"
st="$(settle "$A" owner)"; echo "    $st"
has "owner fresh=1 failed=0" "$st" "the owning node settled fresh"
h="$(handles | python3 -c "
import sys, json
for x in json.load(sys.stdin)['claim_handles']:
    if json.dumps(x.get('claim_scope_data')).find('/west/reports') >= 0: print(json.dumps(x)); break")"
echo "    $h"
has '"lifetime": "durable"' "$h" "the claim is held durably, so it still occupies the scope"
has '"state": "committed"' "$h" "the durable claim handle is committed rather than reaped"

echo "--- a byte-unequal but overlapping scope cannot be claimed while it is held"
B="$(start "$HERE/template-competitor.json" '{"selector":"/east/reports"}')" || bad "competitor template did not register"
st="$(settle "$B" competitor)"; echo "    $st"
has "competitor fresh=0 failed=1" "$st" "the competing writer did not get a claim"
has "signal=terminal/error/acquire/unavailable" "$st" "the competing writer was refused at acquisition"
pl="$(plog)"
sc="$(printf '%s' "$pl" | python3 -c "
import sys, json
for c in json.load(sys.stdin):
    if c['verb'] == 'ScopesConflict': print(c['selector'], '->', c['result'])")"
echo "    $sc"
has "/east/reports ~ /west/reports -> true" "$sc" "rimsky consulted the producer's rule and the producer called the two scopes overlapping"

echo "--- a byte-unequal non-overlapping scope is claimed straight away"
C="$(start "$HERE/template-competitor.json" '{"selector":"/east/invoices"}')" || bad "competitor template did not register"
st="$(settle "$C" competitor)"; echo "    $st"
has "competitor fresh=1 failed=0" "$st" "the non-overlapping writer got its claim"

echo "--- the rule is consulted on the fan-out sub-claim path too"
F="$(start "$HERE/template-fanout.json" '{}')" || bad "fanout template did not register"
subconflicts() {
  printf '%s' "$(plog)" | python3 -c "
import sys, json
for c in json.load(sys.stdin):
    if c['verb'] == 'ScopesConflict' and '/inbox/' in c['selector']: print(c['selector'], '->', c['result'])" | sort -u
}
until [ -n "$(subconflicts)" ]; do sleep 0.3; done
sc="$(subconflicts)"; echo "    $sc"
has "/inbox/b/p1 ~ /inbox/a/p1 -> true" "$sc" "rimsky put the two sub-claim scopes to the producer's rule and the producer called them overlapping"
st="$(node_states "$F")"; echo "    $(printf '%s' "$st" | tr '\n' ' ')"
held="$(handles | python3 -c "
import sys, json
print(sum(1 for h in json.load(sys.stdin)['claim_handles']
          if json.dumps(h.get('claim_scope_data')).find('/inbox/') >= 0))")"
[ "$held" -le 1 ] \
  && ok "the two overlapping sub-claims are not both held: $held of the 2 has a claim handle" \
  || bad "both overlapping sub-claims are held"
printf '%s\n' "$st" | grep -qE '^partitioned fresh=3 failed=0' \
  && bad "every partition settled fresh despite the producer calling their scopes overlapping" \
  || ok "the fan-out did not settle both overlapping partitions"

echo
if [ "$fails" -eq 0 ]; then echo "EXPERIMENT PASS"; else echo "EXPERIMENT FAIL ($fails)"; fi
exit "$fails"
