#!/usr/bin/env bash
# Experiment: commit-response-honored
# A claim producer written for this experiment returns a version id and a
# producer-metadata blob on every base-protocol Commit. A rimsky stack is
# pointed at it. One node takes an ordinary claim; another fans out into two
# sub-claims and a downstream node reads the parent's writeback by attribute
# reference. The run shows the version id on the claim-handle rows and the
# per-partition metadata on the fan-out parent.
set -uo pipefail

TAG="${RIMSKY_IMAGE_TAG:?set RIMSKY_IMAGE_TAG to the image tag built from this tree}"
HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../../.." && pwd)"
CLI="$ROOT/bin/rimsky"
NAME="rimsky-exp-commit-response"
PORT="${PORT:-18206}"
REC_PORT="${REC_PORT:-19469}"
CP_GRPC="${CP_GRPC:-19471}"
CP_HTTP="${CP_HTTP:-19571}"
E="http://127.0.0.1:$PORT"
WORK="$(mktemp -d)"
VERSION_ID="v-42"
META_B64="eyJyb3dzIjo3fQ=="

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
  -name versioned-store -semantics sync -split-scope \
  -version-id "$VERSION_ID" -producer-metadata '{"rows":7}' >"$WORK/producer.log" 2>&1 &
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
  local f="$1" id in
  id="$(register "$f")"
  [ -z "$id" ] && { echo "REGISTER FAILED: $f -- $(curl -sS -XPOST -H 'Content-Type: application/json' -d "{\"spec\": $(cat "$f")}" "$E/v1/templates")" >&2; return 1; }
  "$CLI" template deploy "$id" --endpoint "$E" -o json >/dev/null 2>&1
  in="$("$CLI" instance create "$id" --endpoint "$E" -o json 2>&1 | sed -n 's/.*"instance_id": "\([^"]*\)".*/\1/p')"
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
widest_meta() {
  curl -sS "http://127.0.0.1:$REC_PORT/log" | python3 -c "
import sys, json
best = {}
for e in json.load(sys.stdin):
    if e['path'].startswith('/$1/') and e['path'].endswith('/reader'):
        m = e['body'].get('meta') or {}
        if len(m) > len(best): best = m
print(json.dumps(best))"
}
rec_for() {
  curl -sS "http://127.0.0.1:$REC_PORT/log" | python3 -c "
import sys, json
for e in json.load(sys.stdin):
    if e['path'].endswith('/$1'): print(json.dumps(e['body']))"
}

echo "--- the version id on a base-protocol Commit lands on the claim-handle row"
A="$(start "$HERE/template-version.json")" || bad "the version template did not register"
st="$(settle "$A" writer)"; echo "    $st"
has "writer fresh=1 failed=0" "$st" "the writing node settled fresh"
h="$(handles | python3 -c "
import sys, json
for x in json.load(sys.stdin)['claim_handles']:
    if json.dumps(x.get('claim_scope_data')).find('/versioned') >= 0: print(json.dumps(x)); break")"
echo "    $h"
has "\"version_id\": \"$VERSION_ID\"" "$h" "the claim handle carries the version id the producer returned"
has '"state": "committed"' "$h" "the claim handle is committed"

echo "--- the producer metadata on a sub-claim's Commit lands on the fan-out parent"
B="$(start "$HERE/template-fanout.json")" || bad "the fan-out template did not register"
until node_states "$B" | grep -qE '^partitioned fresh=4'; do sleep 0.3; done
settle "$B" reader >/dev/null
st="$(node_states "$B")"; echo "    $(printf '%s' "$st" | tr '\n' ' ')"
printf '%s\n' "$st" | grep -qE '^reader .*failed=0' \
  && ok "the node reading the parent's writeback settled without failing" \
  || bad "the reading node failed"
body="$(widest_meta rec)"; echo "    producer_metadata on the parent: $body"
for k in p1 p2; do
  has "\"$k\": \"$META_B64\"" "$body" "the parent's writeback carries partition $k's producer metadata under its partition key"
done
n="$(printf '%s' "$body" | python3 -c 'import sys,json; print(len(json.load(sys.stdin)))')"
[ "$n" -ge 2 ] \
  && ok "the writeback is keyed per partition and carries $n of the 3 partitions at the reader's last dispatch" \
  || bad "the writeback carries $n partition entries"

sub="$(handles | python3 -c "
import sys, json
rows = [x for x in json.load(sys.stdin)['claim_handles'] if x.get('parent_claim_handle_id')]
print(json.dumps([{'scope': r.get('claim_scope_data'), 'version_id': r.get('version_id'), 'state': r.get('state')} for r in rows]))")"
echo "    $sub"
has "\"version_id\": \"$VERSION_ID\"" "$sub" "each sub-claim's handle carries the version id its Commit returned"

echo
if [ "$fails" -eq 0 ]; then echo "EXPERIMENT PASS"; else echo "EXPERIMENT FAIL ($fails)"; fi
exit "$fails"
