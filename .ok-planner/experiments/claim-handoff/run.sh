#!/usr/bin/env bash
# Experiment: claim-handoff
# A template declares an acquirer that opens one claim and two downstream
# nodes that co-hold it through the holds directive, each reading the live
# claim's address, a payload field and the scope bytes by alias into its own
# attribute schema. The claim producer keeps a log of every verb it received,
# so the run shows one Open for the whole subgraph and one terminal verb:
# Commit when every holder succeeds, Abandon when the last holder fails.
set -uo pipefail

TAG="${RIMSKY_IMAGE_TAG:?set RIMSKY_IMAGE_TAG to the image tag built from this tree}"
HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../../.." && pwd)"
CLI="$ROOT/bin/rimsky"
NAME="rimsky-exp-claim-handoff"
PORT="${PORT:-18203}"
REC_PORT="${REC_PORT:-19439}"
CP_GRPC="${CP_GRPC:-19441}"
CP_HTTP="${CP_HTTP:-19541}"
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

for p in "$REC_PORT" "$CP_GRPC" "$CP_HTTP" ; do
  if nc -z 127.0.0.1 "$p" >/dev/null 2>&1; then echo "port $p already in use" >&2; exit 2; fi
done

go build -o "$WORK/producer" "$HERE" || { echo "build failed"; exit 1; }
"$WORK/producer" -grpc "127.0.0.1:$CP_GRPC" -http "127.0.0.1:$CP_HTTP" \
  -name stage-store -semantics sync -payload '{"region":"eu-west-1"}' >"$WORK/producer.log" 2>&1 &
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
handle_for() {
  curl -sS "$E/v1/observability/claim-handles?limit=200" | python3 -c "
import sys, json
for h in json.load(sys.stdin)['claim_handles']:
    if json.dumps(h.get('claim_scope_data')).find('$1') >= 0:
        print(json.dumps(h)); break"
}
verbs_for() {
  curl -sS "http://127.0.0.1:$CP_HTTP/log" | python3 -c "
import sys, json
for c in json.load(sys.stdin):
    if c.get('selector') == '$1': print(c['verb'])"
}
rec_for() {
  curl -sS "http://127.0.0.1:$REC_PORT/log" | python3 -c "
import sys, json
for e in json.load(sys.stdin):
    if e['path'].endswith('/$1'): print(json.dumps(e['body']))"
}

echo "--- every holder succeeds: one Open, one Commit, and the claim is committed"
A="$(start "$HERE/template-commit.json" '{"run":"good"}')" || bad "the commit template did not register"
until [ "$(node_states "$A" | grep -c 'fresh=1 failed=0')" = 3 ] || node_states "$A" | grep -q 'failed=[1-9]'; do sleep 0.3; done
st="$(node_states "$A")"; echo "$st"
for t in acquirer writer verifier; do
  has "$t fresh=1 failed=0" "$st" "$t settled fresh"
done

v="$(verbs_for /stage/good)"; echo "    verbs: $(printf '%s' "$v" | tr '\n' ' ')"
[ "$(printf '%s\n' "$v" | grep -c '^Open$')" = 1 ] \
  && ok "the producer received exactly one Open for the whole holding subgraph" \
  || bad "the producer received $(printf '%s\n' "$v" | grep -c '^Open$') Opens"
[ "$(printf '%s\n' "$v" | grep -c '^Commit$')" = 1 ] \
  && ok "the producer received exactly one Commit" \
  || bad "the producer did not receive exactly one Commit"
[ "$(printf '%s\n' "$v" | grep -c '^Abandon$')" = 0 ] \
  && ok "the producer received no Abandon on the all-success run" \
  || bad "the producer received an Abandon on the all-success run"

echo "--- each co-holder read the live claim by alias into its own attributes"
for t in writer verifier; do
  body="$(rec_for "$t")"; echo "    $t: $body"
  has '"held_addr": "stage-store://stage/good"' "$body" "$t read the claim address by alias"
  has '"held_region": "eu-west-1"' "$body" "$t read a payload field of the claim by alias"
  has '"held_scope": "{\"selector\":\"/stage/good\"}"' "$body" "$t read the claim scope bytes by alias"
done
acq="$(rec_for acquirer)"
[ "$(printf '%s' "$acq" | python3 -c 'import sys,json; print(json.load(sys.stdin)["held_addr"])' 2>/dev/null)" \
  = "$(printf '%s' "$(rec_for writer)" | python3 -c 'import sys,json; print(json.load(sys.stdin)["held_addr"])' 2>/dev/null)" ] \
  && ok "the co-holder's address is the acquirer's address, not a re-acquired one" \
  || bad "the co-holder's address differs from the acquirer's"

h="$(handle_for '/stage/good')"; echo "    $h"
has '"state": "committed"' "$h" "the single claim handle ends committed"
hid="$(printf '%s' "$h" | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')"
holders="$(curl -sS "$E/v1/claim-handles/$hid/holders")"; echo "    $holders"
n="$(printf '%s' "$holders" | python3 -c 'import sys,json; d=json.load(sys.stdin); print(len(d.get("holders") or d.get("claim_holders") or []))')"
[ "$n" = 3 ] && ok "the control API reports three holders on the one claim" || bad "holders count is $n, not 3"

echo "--- the last holder fails: the same claim is abandoned across the subgraph"
B="$(start "$HERE/template-abandon.json" '{"run":"bad"}')" || bad "the abandon template did not register"
until node_states "$B" | grep -qE '^verifier .*failed=[1-9]'; do sleep 0.3; done
until node_states "$B" | grep -qE '^acquirer .*failed=[1-9]'; do sleep 0.3; done
st="$(node_states "$B")"; echo "$st"
has "verifier fresh=0 failed=1" "$st" "the verifier failed"
has "acquirer fresh=0 failed=1" "$st" "the acquiring node failed with it"
v="$(verbs_for /stage/bad)"; echo "    verbs: $(printf '%s' "$v" | tr '\n' ' ')"
[ "$(printf '%s\n' "$v" | grep -c '^Open$')" = 1 ] \
  && ok "the failing subgraph still opened the claim exactly once" \
  || bad "the failing subgraph opened the claim more than once"
[ "$(printf '%s\n' "$v" | grep -c '^Abandon$')" -ge 1 ] \
  && ok "the producer received Abandon" \
  || bad "the producer received no Abandon"
[ "$(printf '%s\n' "$v" | grep -c '^Commit$')" = 0 ] \
  && ok "the producer received no Commit for the failed subgraph" \
  || bad "the producer received a Commit despite a failed holder"
h="$(handle_for '/stage/bad')"; echo "    $h"
has '"state": "abandoned"' "$h" "the claim handle ends abandoned"

echo
if [ "$fails" -eq 0 ]; then echo "EXPERIMENT PASS"; else echo "EXPERIMENT FAIL ($fails)"; fi
exit "$fails"
