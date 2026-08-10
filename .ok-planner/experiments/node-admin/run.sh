#!/usr/bin/env bash
# Experiment: node-admin
#
# An operator has an instance with one node that succeeded and one that failed.
# The run checks that, through the operator CLI and the control API:
#   - the node's full state is readable on the running instance (identity,
#     executor, tags, cascade mode, run tallies, latest attributes, and the
#     settled failure marker)
#   - the failed node carries a failure marker the healthy node does not
#   - clearing the marker succeeds on the failed node and is refused on the
#     healthy one
#   - after clearing, the same read reports the node without the marker while
#     every other part of its state is unchanged

set -u
cd "$(dirname "$0")"
ROOT=$(cd ../../.. && pwd)
: "${RIMSKY_IMAGE_TAG:?export RIMSKY_IMAGE_TAG=src-<tree hash> first}"
CLI="$ROOT/bin/rimsky"
NET=exp-nodeadm-net
STACK=exp-nodeadm-stack
SHAPES=exp-nodeadm-shapes
PORT=${PORT:-18931}
WORK=$(mktemp -d)
BASE="http://127.0.0.1:$PORT"

fail=0
note() { printf '%s\n' "$*"; }
check() {
  if [ "$2" = "$3" ]; then printf 'PASS  %-62s %s\n' "$1" "$3"
  else printf 'FAIL  %-62s expected [%s] got [%s]\n' "$1" "$2" "$3"; fail=1; fi
}
cleanup() {
  docker rm -f "$STACK" "$SHAPES" >/dev/null 2>&1
  docker network rm "$NET" >/dev/null 2>&1
  rm -rf "$WORK"
}
trap cleanup EXIT

req() { local m=$1 p=$2 b=${3:-}
  curl -sS -m 15 -w '\n%{http_code}' -X "$m" -H 'content-type: application/json' \
    -H "Idempotency-Key: nodeadm-$RANDOM" ${b:+-d "$b"} "$BASE$p"; }
code() { req "$@" | tail -1; }
body() { req "$@" | sed '$d'; }

note "== a stack with the bundled shape-check executor =="
cat > "$WORK/rimsky.yml" <<'YAML'
persistence:
  driver: sqlite
  sqlite:
    path: /var/lib/rimsky/state.db
claim_producers: {}
named_locks: {}
executors:
  "verifier-shape-checks":
    transport: grpc
    endpoint: "shapes:9095"
    protocols: ["executor"]
YAML
docker network create "$NET" >/dev/null 2>&1
docker rm -f "$STACK" "$SHAPES" >/dev/null 2>&1
docker run -d --name "$SHAPES" --network "$NET" --network-alias shapes \
  -e RIMSKY_EXECUTOR_PORT_GRPC=9095 \
  "rimsky-executor-verifier-shape-checks:$RIMSKY_IMAGE_TAG" >/dev/null || exit 1
docker run -d --name "$STACK" --network "$NET" --network-alias rimsky-stack -p "$PORT:8080" \
  -v "$WORK/rimsky.yml:/etc/rimsky/rimsky.yml:ro" "rimsky-all-in-one:$RIMSKY_IMAGE_TAG" >/dev/null || exit 1
until [ "$(code GET /v1/health)" = 200 ]; do sleep 0.5; done
check "the stack is up" 200 "$(code GET /v1/health)"

note
note "== an instance with one healthy node and one failed node =="
SPEC=$(cat <<'JSON'
{"tag":"nodeadm","spec":{"name":"nodeadm","version":"1","nodes":[
 {"type":"clean","executor":"verifier-shape-checks","tags":["auditable"],
  "attributes":{"schema":{"type":"object","properties":{
    "checks":{"type":"array","default":[{"kind":"no_nulls","config":{"fields":["id"]},"severity":"error"}]},
    "rows":{"type":"array","default":[{"id":1},{"id":2}]}}}}},
 {"type":"broken","executor":"verifier-shape-checks","tags":["auditable"],
  "attributes":{"schema":{"type":"object","properties":{
    "checks":{"type":"array","default":[{"kind":"no_nulls","config":{"fields":["id"]},"severity":"error"}]},
    "rows":{"type":"array","default":[{"id":1},{"id":null}]}}}}}
]}}
JSON
)
TPL=$(body POST /v1/templates "$SPEC" | jq -r '.template_id // empty')
check "template registered" yes "$([ -n "$TPL" ] && echo yes || echo no)"
check "template deployed" 200 "$(code POST "/v1/templates/$TPL/deploy" '{}')"
CREATE=$(body POST /v1/instances "$(printf '{"template":"%s","instance_key":"ck-nodeadm","params":{},"target_agent":"nodeadm-probe"}' "$TPL")")
IID=$(printf '%s' "$CREATE" | jq -r '.instance_id // empty')
check "instance created" yes "$([ -n "$IID" ] && echo yes || echo no)"
[ -n "$IID" ] || note "    create response: $CREATE"
[ -n "$IID" ] || { note "EXPERIMENT FAIL"; exit 1; }
code POST "/v1/instances/$IID/messages" '{"type":""}' >/dev/null

settle() { # settle <node type> -> fresh|failed  (blocks until the node settles)
  local nt=$1 s
  while :; do
    s=$(body GET "/v1/observability/nodes/$IID/$nt" | jq -r '
      .run_summary as $r |
      if $r == null then "in-flight"
      elif $r.failed_count > 0 then "failed"
      elif $r.fresh_count > 0 and $r.active_count == 0 and $r.pending_count == 0 then "fresh"
      else "in-flight" end')
    [ "$s" = in-flight ] || { echo "$s"; return; }
    sleep 0.5
  done
}
note "waiting for both nodes to settle (blocks until they do)"
check "the healthy node settled fresh" fresh "$(settle clean)"
check "the broken node settled failed" failed "$(settle broken)"

note
note "== reading each node's full state =="
NODES=$("$CLI" instance nodes "$IID" --endpoint "$BASE" -o json)
node_id() { printf '%s' "$NODES" | jq -r --arg t "$1" '[.. | objects | select(.node_type == $t) | .id] | first // empty'; }
CLEAN_ID=$(node_id clean)
BROKEN_ID=$(node_id broken)
check "the instance lists both nodes with ids" yes \
  "$([ -n "$CLEAN_ID" ] && [ -n "$BROKEN_ID" ] && echo yes || echo no)"

BROKEN=$(body GET "/v1/nodes/$BROKEN_ID")
note "broken node as the operator reads it:"
printf '%s' "$BROKEN" | jq -c . | sed 's/^/    /'
note "and as the CLI renders it:"
"$CLI" node get "$BROKEN_ID" --endpoint "$BASE" -o json | jq -c . | sed 's/^/    /'
for field in id instance_id node_type executor tags cascade_mode created_at run_summary latest_attributes; do
  check "the read carries the node's $field" yes \
    "$(printf '%s' "$BROKEN" | jq -r --arg f "$field" 'if has($f) then "yes" else "no" end')"
done
check "the broken node's read names its executor" verifier-shape-checks \
  "$(printf '%s' "$BROKEN" | jq -r .executor)"
check "the broken node's read carries its declared tag" auditable \
  "$(printf '%s' "$BROKEN" | jq -r '.tags|join(",")')"
check "the broken node's read tallies one failed run" 1 \
  "$(printf '%s' "$BROKEN" | jq -r '.run_summary.failed_count')"
check "the broken node's read carries the check's own findings" yes \
  "$(printf '%s' "$BROKEN" | jq -r 'if (.latest_attributes|tostring|test("no_nulls")) then "yes" else "no" end')"
MARKER=$(printf '%s' "$BROKEN" | jq -r '.settling_signal_type // ""')
check "the broken node carries a settled failure marker" \
  "terminal/error/verifier/check_failed/no_nulls" "$MARKER"
check "the CLI's own rendering shows the operator the same marker" "$MARKER" \
  "$("$CLI" node get "$BROKEN_ID" --endpoint "$BASE" -o json | jq -r '.settling_signal_type // ""')"

CLEAN=$(body GET "/v1/nodes/$CLEAN_ID")
check "the healthy node's settled signal is a success, not a failure" "terminal/success" \
  "$(printf '%s' "$CLEAN" | jq -r '.settling_signal_type // ""')"
check "the healthy node tallies one fresh run" 1 \
  "$(printf '%s' "$CLEAN" | jq -r '.run_summary.fresh_count')"

note
note "== clearing the stale failure marker =="
check "clearing is refused on a node that never failed" 409 \
  "$(code POST "/v1/nodes/$CLEAN_ID/reset" '{}')"
RESET_OUT=$("$CLI" admin reset "$BROKEN_ID" --endpoint "$BASE" --yes -o json 2>&1)
RESET_RC=$?
note "    admin reset said: $RESET_OUT"
check "clearing the broken node's marker succeeded" 0 "$RESET_RC"

AFTER=$(body GET "/v1/nodes/$BROKEN_ID")
check "the same read now reports no failure marker" "" \
  "$(printf '%s' "$AFTER" | jq -r '.settling_signal_type // ""')"
check "the node is the same node" "$BROKEN_ID" "$(printf '%s' "$AFTER" | jq -r .id)"
check "its executor is unchanged" verifier-shape-checks "$(printf '%s' "$AFTER" | jq -r .executor)"
check "its run history is unchanged" \
  "$(printf '%s' "$BROKEN" | jq -c '.run_summary')" "$(printf '%s' "$AFTER" | jq -c '.run_summary')"
check "the check's findings are still readable" \
  "$(printf '%s' "$BROKEN" | jq -c '.latest_attributes')" "$(printf '%s' "$AFTER" | jq -c '.latest_attributes')"
check "the clearing is on the record as an operator override" 1 \
  "$(body GET "/v1/events?instance_id=$IID&limit=200" | jq '[.events[]? // empty | select((.kind|tostring|test("operator")) and (.node_id==$n))] | length' --arg n "$BROKEN_ID")"

note
if [ "$fail" = 0 ]; then note "EXPERIMENT PASS"; else note "EXPERIMENT FAIL"; fi
exit "$fail"
