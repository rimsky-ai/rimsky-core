#!/usr/bin/env bash
# Experiment: runtime-diagnostics
#
# Wedges an instance on purpose in the four ways the story names, then asks the
# product — never the database — why it is stuck:
#
#   a node parked and not coming back   -> the park roster, and `rimsky parked list`
#   a receiver waiting on that upstream -> the wait-set for its frame
#   a frame gripped by the wedge        -> the held-frame roster
#   a claim nobody has given back       -> the claim's current holders
#
# The executor is peer/, reused from the permissive-peer-build experiment, so a
# node can be told to park while holding a producer claim. The claim producer is
# the bundled filesystem one. Nothing here opens the store.

set -u
cd "$(dirname "$0")"
ROOT=$(cd ../../.. && pwd)
: "${RIMSKY_IMAGE_TAG:?export RIMSKY_IMAGE_TAG=src-<tree hash> first}"
NET=exp-diag-net
STACK=exp-diag-stack
PEER=exp-diag-peer
PROD=exp-diag-producer
free_port() { python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()'; }
PORT=${PORT:-$(free_port)}
WORK=$(mktemp -d)
BASE="http://127.0.0.1:$PORT"

fail=0
note() { printf '%s\n' "$*"; }
check() {
  if [ "$2" = "$3" ]; then printf 'PASS  %-64s %s\n' "$1" "$3"
  else printf 'FAIL  %-64s expected [%s] got [%s]\n' "$1" "$2" "$3"; fail=1; fi
}
cleanup() {
  docker rm -f "$STACK" "$PEER" "$PROD" >/dev/null 2>&1
  docker network rm "$NET" >/dev/null 2>&1
  rm -rf "$WORK"
}
trap cleanup EXIT

req() { local m=$1 p=$2 b=${3:-}
  curl -sS -m 20 -w '\n%{http_code}' -X "$m" -H 'content-type: application/json' \
    -H "Idempotency-Key: dg-$RANDOM$RANDOM" ${b:+-d "$b"} "$BASE$p"; }
code() { req "$@" | tail -1; }
body() { req "$@" | sed '$d'; }

note "== build the executor and the CLI, and bring up a stack with a producer =="
cp -r peer "$WORK/peer"
sed "s|RIMSKY_PROTOCOLS_PATH|$ROOT/lib/protocols|" "$WORK/peer/go.mod.tmpl" > "$WORK/peer/go.mod"
rm "$WORK/peer/go.mod.tmpl"
ARCH=$(docker info --format '{{.Architecture}}' | sed 's/aarch64/arm64/;s/x86_64/amd64/')
( cd "$WORK/peer" && GOWORK=off GOOS=linux GOARCH="$ARCH" CGO_ENABLED=0 GOFLAGS=-mod=mod GOPROXY=off GOSUMDB=off go build -o "$WORK/peer-linux" . ) || { note "peer build failed"; exit 1; }
CLI="$ROOT/bin/rimsky"
[ -x "$CLI" ] && check "the released CLI binary is present" yes yes \
  || check "the released CLI binary is present" yes no

mkdir -p "$WORK/fsdata/notes" && printf 'content\n' > "$WORK/fsdata/notes/a.txt"
cat > "$WORK/producer.yml" <<'YAML'
root: /data
host: 0.0.0.0
grpc_port: 9100
http_port: 9110
YAML
cat > "$WORK/rimsky.yml" <<'YAML'
persistence:
  driver: sqlite
  sqlite:
    path: /var/lib/rimsky/state.db
claim_producers:
  files:
    endpoint: "producer:9100"
    protocols: ["claim_producer"]
    write_semantics_allowed: ["sync"]
named_locks: {}
executors:
  "third-party":
    transport: grpc
    endpoint: "peer:9400"
    protocols: ["executor"]
YAML
docker network create "$NET" >/dev/null 2>&1
docker rm -f "$STACK" "$PEER" "$PROD" >/dev/null 2>&1
docker run -d --name "$PEER" --network "$NET" --network-alias peer \
  -e PEER_PORT=9400 -v "$WORK/peer-linux:/peer:ro" alpine:latest /peer >/dev/null || exit 1
docker run -d --name "$PROD" --network "$NET" --network-alias producer \
  -e RIMSKY_CLAIM_PRODUCER_FILESYSTEM_CONFIG=/etc/producer.yml \
  -v "$WORK/producer.yml:/etc/producer.yml:ro" -v "$WORK/fsdata:/data" \
  "rimsky-claim-producer-filesystem:$RIMSKY_IMAGE_TAG" >/dev/null || exit 1
docker run -d --name "$STACK" --network "$NET" -p "$PORT:8080" \
  -v "$WORK/rimsky.yml:/etc/rimsky/rimsky.yml:ro" "rimsky-all-in-one:$RIMSKY_IMAGE_TAG" >/dev/null || exit 1
until [ "$(code GET /v1/health)" = 200 ]; do sleep 0.5; done
until [ "$(body GET /v1/observability/executors/third-party | jq -r '.peer.reachability_status')" = reachable ]; do sleep 0.5; done
until [ "$(body GET /v1/observability/claim-producers | jq -r '.claim_producers[0].reachability_status')" = reachable ]; do sleep 0.5; done

note
note "== wedge an instance: a claim-holding node parks, and a receiver waits on it =="
SPEC='{"tag":"diag","spec":{"name":"diag","version":"1",
 "messages":[{"type":"never/sent","body_schema":{"type":"object"}}],
 "nodes":[
  {"type":"trigger","kind":"loop_counter","attributes":{"schema":{"type":"object","properties":{"max":{"type":"integer","default":1},"count":{"type":"integer"}}}}},
  {"type":"holder","executor":"third-party","claim_producers":[{"name":"files","selector":"notes/a.txt","intent":"rw"}],"error_types":{"acquire/unavailable":{"action":"retry"}},"subscribes":[{"node":"never/sent","type":"terminal/success","force_upstream_refresh":false}],"attributes":{"schema":{"type":"object","properties":{"outcome":{"type":"string","default":"park"},"echo":{"type":"string","default":"held"}}}}},
  {"type":"receiver","kind":"attribute_passthrough","subscribes":[{"node":"trigger","type":"terminal/success","force_upstream_refresh":false},{"node":"holder","type":"attribute/echo/changed","force_upstream_refresh":true}],"attributes":{"schema":{"type":"object","properties":{"seen":{"type":"integer","default":1}}}}},
  {"type":"member","executor":"third-party","holds":{"files":{"from":"holder"}},"subscribes":[{"node":"holder","type":"terminal/success","force_upstream_refresh":false}],"attributes":{"schema":{"type":"object","properties":{"outcome":{"type":"string","default":"ok"},"echo":{"type":"string","default":"member"}}}}}
]}}'
REG=$(body POST /v1/templates "$SPEC")
TPL=$(printf '%s' "$REG" | jq -r '.template_id // empty')
check "template registered" yes "$([ -n "$TPL" ] && echo yes || echo no)"
[ -n "$TPL" ] || { note "register response: $REG"; note "EXPERIMENT FAIL"; exit 1; }
check "template deployed" 200 "$(code POST "/v1/templates/$TPL/deploy" '{}')"
IID=$(body POST /v1/instances "{\"template\":\"$TPL\",\"instance_key\":\"diag-1\",\"params\":{},\"target_agent\":\"diag-agent\"}" | jq -r '.instance_id // empty')
check "instance created" yes "$([ -n "$IID" ] && echo yes || echo no)"
[ -n "$IID" ] || { note "EXPERIMENT FAIL"; exit 1; }
code POST "/v1/instances/$IID/messages" '{"type":""}' >/dev/null

note "waiting for the wedge to form: the claim-holding node parks (blocks until it does)"
until [ "$(body GET /v1/admin/diagnostics/parked-nodes | jq -r --arg i "$IID" '[.parked_nodes[]?|select(.instance_id==$i)]|length')" -ge 1 ]; do sleep 0.5; done

note
note "== which nodes are parked =="
PARKED=$(body GET /v1/admin/diagnostics/parked-nodes | jq -c --arg i "$IID" '[.parked_nodes[]?|select(.instance_id==$i)]')
PARKED_ID=$(printf '%s' "$PARKED" | jq -r '.[0].node_id')
check "the park roster names exactly the wedged node" 1 "$(printf '%s' "$PARKED" | jq 'length')"
check "the roster's node is the claim holder" holder "$(body GET "/v1/nodes/$PARKED_ID" | jq -r '.node_type')"
check "the roster says when it parked and when it is due back" yes \
  "$(printf '%s' "$PARKED" | jq -r 'if (.[0].parked_at|length)>0 and (.[0].resume_at|length)>0 then "yes" else "no" end')"
check "the CLI shows the same row" 1 \
  "$("$CLI" parked list --endpoint "$BASE" --instance "$IID" -o json | jq 'length')"
check "the CLI names the same node" "$PARKED_ID" \
  "$("$CLI" parked list --endpoint "$BASE" --instance "$IID" -o json | jq -r '.[0].node_id')"

note
note "== which frames the wedge is gripping =="
HELD=$(body GET /v1/admin/diagnostics/held-frames | jq -c --arg i "$IID" '[.frames[]?|select(.instance_id==$i)]')
check "a held frame is reported for this instance" 1 "$(printf '%s' "$HELD" | jq 'length')"
check "the held frame names the parked node it is waiting on" "$PARKED_ID" \
  "$(printf '%s' "$HELD" | jq -r '.[0].node_ids|join(",")')"
check "the held frame reports that node's state" parked "$(printf '%s' "$HELD" | jq -r '.[0].node_states[0].state')"
check "the held frame says how long it has been held" yes \
  "$(printf '%s' "$HELD" | jq -r 'if (.[0].held_since|length)>0 then "yes" else "no" end')"
FRAME=$(printf '%s' "$HELD" | jq -r '.[0].frame_id')
check "the same frame is listed on the instance" yes \
  "$(body GET "/v1/instances/$IID/frames" | jq -r --arg f "$FRAME" 'if ([.frames[]?|select(.frame_id==$f or .id==$f)]|length)>0 then "yes" else "no" end')"

note
note "== which wake dependencies are still pending =="
note "waiting for the receiver's dependency on the parked node to be recorded (blocks until it is)"
until [ "$(body GET "/v1/admin/diagnostics/wait-sets?frame=$FRAME" | jq '[.wait_set[]?]|length')" -ge 1 ]; do sleep 0.5; done
WAIT=$(body GET "/v1/admin/diagnostics/wait-sets?frame=$FRAME" | jq -c '.wait_set')
note "wait-set: $WAIT"
check "the frame's wait-set has entries" yes "$([ "$(printf '%s' "$WAIT" | jq 'length')" -ge 1 ] && echo yes || echo no)"
check "every entry names a sender and a receiver run" 0 \
  "$(printf '%s' "$WAIT" | jq '[.[]|select((.sender_run_id|length)==0 or (.receiver_run_id|length)==0)]|length')"
check "every entry names what it is waiting for" 0 \
  "$(printf '%s' "$WAIT" | jq '[.[]|select((.topic_kind|length)==0)]|length')"
check "the wait-set route refuses to answer without a frame" 400 "$(code GET /v1/admin/diagnostics/wait-sets)"
check "the receiver has not run while the dependency is pending" 0 \
  "$(body GET "/v1/observability/nodes/$IID/receiver" | jq '(.run_summary.fresh_count // 0) + (.run_summary.failed_count // 0)')"

note
note "== who is holding the claim =="
CH=$(body GET "/v1/observability/claim-handles?instance_id=$IID" | jq -c '.claim_handles')
check "the instance's claim handle is visible" 1 "$(printf '%s' "$CH" | jq 'length')"
CHID=$(printf '%s' "$CH" | jq -r '.[0].id')
HOLDERS=$(body GET "/v1/claim-handles/$CHID/holders")
note "holders: $(printf '%s' "$HOLDERS" | jq -c '.')"
check "the claim reports a current holder" yes \
  "$([ "$(printf '%s' "$HOLDERS" | jq '[.holders[]?]|length')" -ge 1 ] && echo yes || echo no)"
check "every holder row names its run and its state" 0 \
  "$(printf '%s' "$HOLDERS" | jq '[.holders[]?|select((.holder_run_id|length)==0 or (.state|length)==0)]|length')"
check "the holder is still active, so the claim is not coming back yet" active \
  "$(printf '%s' "$HOLDERS" | jq -r '[.holders[]?.state]|unique|join(",")')"

note
if [ "$fail" = 0 ]; then note "EXPERIMENT PASS"; else note "EXPERIMENT FAIL"; fi
exit "$fail"
