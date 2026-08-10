#!/usr/bin/env bash
# Experiment: work-completed-emitted
#
# Drives one instance whose six dispatches take every disposition available —
# success, error, error-then-retry, a park that resumes and then succeeds, a
# park that stays outstanding, and a built-in executor's dispatch — then reads
# the instance's event log through the public feed and pairs it: every settled
# dispatch id carries a work_completed naming its terminal kind, each pair
# yields a non-negative duration from the two timestamps alone, and the single
# unpaired start is exactly the dispatch that has not finished.
#
# The executor is peer/, reused from the permissive-peer-build experiment, so
# the run can choose the disposition of each dispatch from the template.

set -u
cd "$(dirname "$0")"
ROOT=$(cd ../../.. && pwd)
: "${RIMSKY_IMAGE_TAG:?export RIMSKY_IMAGE_TAG=src-<tree hash> first}"
NET=exp-workpair-net
STACK=exp-workpair-stack
PEER=exp-workpair-peer
PORT=${PORT:-19312}
WORK=$(mktemp -d)
BASE="http://127.0.0.1:$PORT"

fail=0
note() { printf '%s\n' "$*"; }
check() {
  if [ "$2" = "$3" ]; then printf 'PASS  %-64s %s\n' "$1" "$3"
  else printf 'FAIL  %-64s expected [%s] got [%s]\n' "$1" "$2" "$3"; fail=1; fi
}
cleanup() {
  docker rm -f "$STACK" "$PEER" >/dev/null 2>&1
  docker network rm "$NET" >/dev/null 2>&1
  rm -rf "$WORK"
}
trap cleanup EXIT

req() { local m=$1 p=$2 b=${3:-}
  curl -sS -m 20 -w '\n%{http_code}' -X "$m" -H 'content-type: application/json' \
    -H "Idempotency-Key: wc-$RANDOM$RANDOM" ${b:+-d "$b"} "$BASE$p"; }
code() { req "$@" | tail -1; }
body() { req "$@" | sed '$d'; }

note "== build the executor and bring up a stack =="
cp -r peer "$WORK/peer"
sed "s|RIMSKY_PROTOCOLS_PATH|$ROOT/lib/protocols|" "$WORK/peer/go.mod.tmpl" > "$WORK/peer/go.mod"
rm "$WORK/peer/go.mod.tmpl"
ARCH=$(docker info --format '{{.Architecture}}' | sed 's/aarch64/arm64/;s/x86_64/amd64/')
( cd "$WORK/peer" && GOOS=linux GOARCH="$ARCH" CGO_ENABLED=0 GOFLAGS=-mod=mod GOPROXY=off GOSUMDB=off go build -o "$WORK/peer-linux" . ) || { note "peer build failed"; exit 1; }

cat > "$WORK/rimsky.yml" <<'YAML'
persistence:
  driver: sqlite
  sqlite:
    path: /var/lib/rimsky/state.db
claim_producers: {}
named_locks: {}
executors:
  "third-party":
    transport: grpc
    endpoint: "peer:9400"
    protocols: ["executor"]
YAML
docker network create "$NET" >/dev/null 2>&1
docker rm -f "$STACK" "$PEER" >/dev/null 2>&1
docker run -d --name "$PEER" --network "$NET" --network-alias peer \
  -e PEER_PORT=9400 -v "$WORK/peer-linux:/peer:ro" alpine:latest /peer >/dev/null || exit 1
docker run -d --name "$STACK" --network "$NET" -p "$PORT:8080" \
  -v "$WORK/rimsky.yml:/etc/rimsky/rimsky.yml:ro" "rimsky-all-in-one:$RIMSKY_IMAGE_TAG" >/dev/null || exit 1
until [ "$(code GET /v1/health)" = 200 ]; do sleep 0.5; done
until [ "$(body GET /v1/observability/executors/third-party | jq -r '.peer.reachability_status')" = reachable ]; do sleep 0.5; done

note
note "== one instance whose six dispatches take six dispositions =="
peer_node() { printf '{"type":"%s","executor":"third-party"%s,"attributes":{"schema":{"type":"object","properties":{"outcome":{"type":"string","default":"%s"},"echo":{"type":"string","default":"%s"}}}}}' "$1" "$3" "$2" "$1"; }
SPEC=$(printf '{"tag":"workpair","spec":{"name":"workpair","version":"1","nodes":[%s,%s,%s,%s,%s,%s]}}' \
  "$(peer_node ok ok '')" \
  "$(peer_node failing fail ',"error_types":{"third-party/refused":{"action":"give_up"}}')" \
  "$(peer_node retrying broken ',"max_retries":2,"error_types":{"third-party/broken":{"action":"retry"}}')" \
  "$(peer_node parking park_once '')" \
  "$(peer_node outstanding park '')" \
  '{"type":"counter","kind":"loop_counter","attributes":{"schema":{"type":"object","properties":{"max":{"type":"integer","default":1},"count":{"type":"integer"}}}}}')
REG=$(body POST /v1/templates "$SPEC")
TPL=$(printf '%s' "$REG" | jq -r '.template_id // empty')
check "template registered" yes "$([ -n "$TPL" ] && echo yes || echo no)"
[ -n "$TPL" ] || { note "register response: $REG"; note "EXPERIMENT FAIL"; exit 1; }
check "template deployed" 200 "$(code POST "/v1/templates/$TPL/deploy" '{}')"
IID=$(body POST /v1/instances "{\"template\":\"$TPL\",\"instance_key\":\"workpair-1\",\"params\":{},\"target_agent\":\"workpair-agent\"}" | jq -r '.instance_id // empty')
check "instance created" yes "$([ -n "$IID" ] && echo yes || echo no)"
[ -n "$IID" ] || { note "EXPERIMENT FAIL"; exit 1; }
code POST "/v1/instances/$IID/messages" '{"type":""}' >/dev/null

events() { body GET "/v1/events?instance_id=$IID&limit=1000" | jq '.events'; }
note "waiting for the five settling dispatches to complete (blocks until they do)"
until [ "$(events | jq '[.[]|select(.kind=="work_completed")]|length')" -ge 5 ]; do sleep 0.5; done
note "waiting for the sixth dispatch to be parked and outstanding (blocks until it is)"
until [ "$(body GET /v1/admin/diagnostics/parked-nodes | jq -r --arg i "$IID" '[.parked_nodes[]?|select(.instance_id==$i)]|length')" -ge 1 ]; do sleep 0.5; done
EV=$(events)

note
note "== pair the ledger =="
STARTED=$(printf '%s' "$EV" | jq '[.[]|select(.kind=="work_started")]|length')
COMPLETED=$(printf '%s' "$EV" | jq '[.[]|select(.kind=="work_completed")]|length')
note "work_started events=$STARTED work_completed events=$COMPLETED"
check "no work_completed names a dispatch that never started" '[]' \
  "$(printf '%s' "$EV" | jq -c '([.[]|select(.kind=="work_completed")|.payload.dispatch_id]|unique) - ([.[]|select(.kind=="work_started")|.payload.dispatch_id]|unique)')"
check "every settled dispatch carries a work_completed" 5 \
  "$(printf '%s' "$EV" | jq '[.[]|select(.kind=="work_completed")|.payload.dispatch_id]|unique|length')"
check "every completion names its terminal kind" 0 \
  "$(printf '%s' "$EV" | jq '[.[]|select(.kind=="work_completed")|select((.payload.terminal_kind//"")=="")]|length')"
check "the terminal kinds distinguish success from failure" "complete,errored" \
  "$(printf '%s' "$EV" | jq -r '[.[]|select(.kind=="work_completed")|.payload.terminal_kind]|unique|sort|join(",")')"

note
note "the one unpaired start is the dispatch that has not finished"
UNPAIRED=$(printf '%s' "$EV" | jq -c '([.[]|select(.kind=="work_completed")|.payload.dispatch_id]|unique) as $c | [.[]|select(.kind=="work_started")|. as $w|select(($c|index($w.payload.dispatch_id))==null)]')
check "exactly one started dispatch has no completion" 1 "$(printf '%s' "$UNPAIRED" | jq '[.[]|.payload.dispatch_id]|unique|length')"
check "that dispatch is the one the park roster still holds" yes \
  "$([ "$(printf '%s' "$UNPAIRED" | jq -r '.[0].node_id')" = "$(body GET /v1/admin/diagnostics/parked-nodes | jq -r --arg i "$IID" '[.parked_nodes[]?|select(.instance_id==$i)|.node_id]|first')" ] && echo yes || echo no)"
check "the outstanding dispatch is the node the template asked to park" outstanding \
  "$(body GET "/v1/nodes/$(printf '%s' "$UNPAIRED" | jq -r '.[0].node_id')" | jq -r '.node_type')"

note
note "== durations are computable from the two timestamps alone =="
DUR=$(printf '%s' "$EV" | jq -r '
  ([.[]|select(.kind=="work_started")]|group_by(.payload.dispatch_id)|map({id:.[0].payload.dispatch_id, at:(map(.occurred_at)|min)})) as $s |
  ([.[]|select(.kind=="work_completed")]|group_by(.payload.dispatch_id)|map({id:.[0].payload.dispatch_id, at:(map(.occurred_at)|max)})) as $c |
  [$s[] as $x | ($c[]|select(.id==$x.id)) as $y |
     {id:$x.id, secs: (($y.at|sub("\\.[0-9]+Z$";"Z")|fromdate) - ($x.at|sub("\\.[0-9]+Z$";"Z")|fromdate))}]')
note "$(printf '%s' "$DUR" | jq -c '[.[]|{id:(.id[0:8]),secs}]')"
check "every pair yields a non-negative duration" 0 "$(printf '%s' "$DUR" | jq '[.[]|select(.secs<0)]|length')"
check "a duration was computed for every completed dispatch" 5 "$(printf '%s' "$DUR" | jq 'length')"

note
note "the ledger's dispatch dispositions:"
printf '%s' "$EV" | jq -r '.[]|select(.kind=="work_completed")|"    \(.payload.dispatch_id[0:8]) \(.payload.terminal_kind)"'

note
if [ "$fail" = 0 ]; then note "EXPERIMENT PASS"; else note "EXPERIMENT FAIL"; fi
exit "$fail"
