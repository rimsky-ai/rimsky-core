#!/usr/bin/env bash
# Experiment: claim-producer-filesystem
# The bundled filesystem claim producer is configured over a bind-mounted host
# directory and a rimsky stack is booted on it. One node takes a claim on a
# directory under the root and its executor writes through the address the
# producer handed back; another node fans out over the same store's own
# contents. The run shows the address resolving to the directory itself, the
# write landing there with nothing else appearing under the root, and one
# sub-claim per file already present in the store.
set -uo pipefail

TAG="${RIMSKY_IMAGE_TAG:?set RIMSKY_IMAGE_TAG to the image tag built from this tree}"
HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../../.." && pwd)"
CLI="$ROOT/bin/rimsky"
NAME="rimsky-exp-claim-producer-filesystem"
PORT="${PORT:-18202}"
REC_PORT="${REC_PORT:-19429}"
E="http://127.0.0.1:$PORT"
WS="$(mktemp -d)"

fails=0
ok()  { echo "PASS  $*"; }
bad() { echo "FAIL  $*"; fails=$((fails+1)); }
has() { if printf '%s' "$2" | grep -qF -- "$1"; then ok "$3"; else bad "$3 (missing '$1')"; fi; }

PIDS=()
cleanup() {
  for p in "${PIDS[@]:-}"; do kill "$p" >/dev/null 2>&1; done
  docker rm -f "$NAME" >/dev/null 2>&1
  rm -rf "$WS"
}
trap cleanup EXIT

mkdir -p "$WS/data/reports" "$WS/data/inbox"
printf 'a\n' > "$WS/data/inbox/alpha.txt"
printf 'b\n' > "$WS/data/inbox/beta.txt"
printf 'c\n' > "$WS/data/inbox/gamma.txt"
before="$(cd "$WS" && find . | sort)"

if nc -z 127.0.0.1 "$REC_PORT" >/dev/null 2>&1; then
  echo "port $REC_PORT is already in use" >&2; exit 2
fi
python3 "$HERE/writer.py" "$REC_PORT" "$WS" >"$WS/.writer.log" 2>&1 &
PIDS+=("$!")
until curl -sS "http://127.0.0.1:$REC_PORT/log" >/dev/null 2>&1; do sleep 0.1; done

docker rm -f "$NAME" >/dev/null 2>&1
docker run -d --name "$NAME" -p "127.0.0.1:$PORT:8080" \
  -e RIMSKY_CLAIM_PRODUCER_FILESYSTEM_CONFIG=/etc/rimsky/claim-producer-filesystem.yml \
  -e RIMSKY_EXECUTOR_HTTP_NODE_EGRESS_ALLOWLIST=0.0.0.0/0 \
  -v "$HERE/claim-producer-filesystem.yml:/etc/rimsky/claim-producer-filesystem.yml:ro" \
  -v "$WS:/workspace:rw" \
  "rimsky-all-in-one:$TAG" >/dev/null
until "$CLI" health --endpoint "$E" >/dev/null 2>&1; do sleep 0.2; done

docker logs "$NAME" 2>&1 | grep -q 'bundled claim producer registered in-process.*claim-producer-filesystem' \
  && ok "the bundled filesystem claim producer registers over the configured root" \
  || bad "the bundled filesystem claim producer did not register"

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
events() {
  "$CLI" instance events "$1" --endpoint "$E" -o json 2>/dev/null | python3 -c '
import sys, json
raw = sys.stdin.read(); dec = json.JSONDecoder(); i = 0
while i < len(raw):
    while i < len(raw) and raw[i] in " \n\t\r": i += 1
    if i >= len(raw): break
    o, j = dec.raw_decode(raw, i); i = j
    print(o.get("kind"), json.dumps(o.get("payload")))'
}

echo "--- a claim on a directory under the root resolves to that directory"
A="$(start "$HERE/template-inplace.json")" || bad "the in-place template did not register"
until node_states "$A" | grep -qE '^writer fresh=1|^writer failed=[1-9]'; do sleep 0.3; done
st="$(node_states "$A")"; echo "$st"
has "writer fresh=1 failed=0" "$st" "the writing node settled fresh"
row="$(curl -sS "$E/v1/observability/claim-handles?limit=100" | python3 -c "
import sys, json
for h in json.load(sys.stdin)['claim_handles']:
    if h.get('claim_scope_data') and 'reports' in json.dumps(h['claim_scope_data']):
        print(json.dumps(h)); break")"
echo "    $row"
has '"realized_write_semantics": "sync"' "$row" "the producer realizes the claim as a synchronous write"
has '"state": "committed"' "$row" "the claim was committed through the producer"

rec="$(curl -sS "http://127.0.0.1:$REC_PORT/log")"
echo "    $rec"
has "\"held_addr\": \"/workspace/data/reports\"" "$rec" "the address the executor received is the claimed directory itself"

echo "--- the write lands at the address, in place, with nothing else appearing under the root"
[ -f "$WS/data/reports/out.txt" ] \
  && ok "the file the executor wrote is at the claimed directory on the host" \
  || bad "no file at $WS/data/reports/out.txt"
[ "$(cat "$WS/data/reports/out.txt" 2>/dev/null)" = "written-in-place" ] \
  && ok "the bytes at the address are the bytes the executor wrote" \
  || bad "content mismatch at the address"
after="$(cd "$WS" && find . | sort)"
extra="$(comm -13 <(printf '%s\n' "$before") <(printf '%s\n' "$after") | grep -v '^\./data/reports/out\.txt$' | grep -v '^\./\.writer\.log$')"
if [ -z "$extra" ]; then
  ok "the commit added no staging directory and no swapped-in copy under the root"
else
  bad "the root gained entries other than the written file: $extra"
fi

echo "--- a fan-out partitions the store's own contents"
B="$(start "$HERE/template-expand.json")" || bad "the expand-folder template did not register"
until node_states "$B" | grep -qE '^partitioned fresh=4|^partitioned fresh=0 failed=[1-9]'; do sleep 0.3; done
st="$(node_states "$B")"; echo "$st"
has "partitioned fresh=4 failed=0" "$st" "the parent and one work unit per file in the store settled fresh"
ev="$(events "$B")"
has '"sub_scope_descriptor_count": 3' "$ev" "the producer's split returned one sub-scope per file already in the store"
has '"child_keys": ["alpha.txt", "beta.txt", "gamma.txt"]' "$ev" "the partition keys are the store's own file names"
rec="$(curl -sS "http://127.0.0.1:$REC_PORT/log")"
for f in alpha.txt beta.txt gamma.txt; do
  has "\"partition\": \"$f\"" "$rec" "a work unit ran for $f"
  has "\"held_addr\": \"/workspace/data/inbox/$f\"" "$rec" "that work unit's claim addresses $f itself"
done

echo "--- the whole claim substrate is the host directory"
echo "    $(cd "$WS" && find . -not -name '.writer.log' | sort | tr '\n' ' ')"

echo
if [ "$fails" -eq 0 ]; then echo "EXPERIMENT PASS"; else echo "EXPERIMENT FAIL ($fails)"; fi
exit "$fails"
