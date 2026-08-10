#!/usr/bin/env bash
# Experiment: executor-trace-observability
#
# Stands in for an operator's dashboard: it learns the executor's observability
# endpoint from the control API, learns the dispatch id from the event feed,
# and then talks the executor-observability protocol itself (client/, a
# standalone module whose only rimsky dependency is the protocols module).
#
# The dispatch is held open on purpose — the node's HTTP call lands on
# slowserver/, which blocks until this run releases it — so "in flight" is a
# fact the run establishes rather than a wall-clock guess:
#
#   stream while in flight  -> events arrive before the dispatch can finish
#   the stream stays live   -> the terminal event arrives only after release
#   fetch after the fact    -> the whole record comes back, marked complete
#   the records are structured, not log lines

set -u
cd "$(dirname "$0")"
ROOT=$(cd ../../.. && pwd)
: "${RIMSKY_IMAGE_TAG:?export RIMSKY_IMAGE_TAG=src-<tree hash> first}"
NET=exp-trace-net
STACK=exp-trace-stack
EXEC=exp-trace-executor
SLOW=exp-trace-slow
PORT=${PORT:-19315}
EXEC_PORT=${EXEC_PORT:-19325}
SLOW_PORT=${SLOW_PORT:-19335}
WORK=$(mktemp -d)
BASE="http://127.0.0.1:$PORT"
OBS="127.0.0.1:$EXEC_PORT"

fail=0
note() { printf '%s\n' "$*"; }
check() {
  if [ "$2" = "$3" ]; then printf 'PASS  %-64s %s\n' "$1" "$3"
  else printf 'FAIL  %-64s expected [%s] got [%s]\n' "$1" "$2" "$3"; fail=1; fi
}
cleanup() {
  [ -n "${STREAM_PID:-}" ] && kill "$STREAM_PID" >/dev/null 2>&1
  docker rm -f "$STACK" "$EXEC" "$SLOW" >/dev/null 2>&1
  docker network rm "$NET" >/dev/null 2>&1
  rm -rf "$WORK"
}
trap cleanup EXIT

req() { local m=$1 p=$2 b=${3:-}
  curl -sS -m 20 -w '\n%{http_code}' -X "$m" -H 'content-type: application/json' \
    -H "Idempotency-Key: tr-$RANDOM$RANDOM" ${b:+-d "$b"} "$BASE$p"; }
code() { req "$@" | tail -1; }
body() { req "$@" | sed '$d'; }

note "== build the dashboard client and the holdable endpoint =="
ARCH=$(docker info --format '{{.Architecture}}' | sed 's/aarch64/arm64/;s/x86_64/amd64/')
cp -r client "$WORK/client"
sed "s|RIMSKY_PROTOCOLS_PATH|$ROOT/lib/protocols|" "$WORK/client/go.mod.tmpl" > "$WORK/client/go.mod"
rm "$WORK/client/go.mod.tmpl"
( cd "$WORK/client" && GOWORK=off GOFLAGS=-mod=mod GOPROXY=off GOSUMDB=off go build -o "$WORK/trace-client" . ) \
  && check "the dashboard client builds against the protocols module alone" yes yes \
  || { check "the dashboard client builds against the protocols module alone" yes no; note "EXPERIMENT FAIL"; exit 1; }
cp -r slowserver "$WORK/slowserver"
( cd "$WORK/slowserver" && GOWORK=off GOOS=linux GOARCH="$ARCH" CGO_ENABLED=0 GOFLAGS=-mod=mod GOPROXY=off go build -o "$WORK/slow-linux" . ) || exit 1

note
note "== bring up the stack with the bundled http-node executor =="
docker network create "$NET" >/dev/null 2>&1
SUBNET=$(docker network inspect "$NET" -f '{{(index .IPAM.Config 0).Subnet}}')
note "docker network subnet: $SUBNET (opened in the executor's egress allowlist)"
cat > "$WORK/rimsky.yml" <<'YAML'
persistence:
  driver: sqlite
  sqlite:
    path: /var/lib/rimsky/state.db
claim_producers: {}
named_locks: {}
executors:
  "http":
    transport: grpc
    endpoint: "executor:9091"
    protocols: ["executor"]
YAML
docker rm -f "$STACK" "$EXEC" "$SLOW" >/dev/null 2>&1
docker run -d --name "$SLOW" --network "$NET" --network-alias slow -p "$SLOW_PORT:8000" \
  -v "$WORK/slow-linux:/slow:ro" alpine:latest /slow >/dev/null || exit 1
docker run -d --name "$EXEC" --network "$NET" --network-alias executor -p "$EXEC_PORT:9091" \
  -e RIMSKY_EXECUTOR_HTTP_NODE_EGRESS_ALLOWLIST="$SUBNET" \
  "rimsky-executor-http-node:$RIMSKY_IMAGE_TAG" >/dev/null || exit 1
docker run -d --name "$STACK" --network "$NET" -p "$PORT:8080" \
  -v "$WORK/rimsky.yml:/etc/rimsky/rimsky.yml:ro" "rimsky-all-in-one:$RIMSKY_IMAGE_TAG" >/dev/null || exit 1
until [ "$(code GET /v1/health)" = 200 ]; do sleep 0.5; done
until curl -sf -m 5 "http://127.0.0.1:$SLOW_PORT/status" >/dev/null; do sleep 0.5; done
until [ "$(body GET /v1/observability/executors/http | jq -r '.peer.reachability_status')" = reachable ]; do sleep 0.5; done

note
note "== the operator learns the executor supports traces, from the control API =="
CAPS=$(body GET /v1/observability/executors/http)
check "the control API names the executor's observability endpoint" "executor:9091" \
  "$(printf '%s' "$CAPS" | jq -r '.peer.observability_endpoint')"
check "the executor advertises trace fetch" true "$(printf '%s' "$CAPS" | jq -r '.peer.observability_capabilities.supports_trace_get')"
check "the executor advertises trace streaming" true "$(printf '%s' "$CAPS" | jq -r '.peer.observability_capabilities.supports_trace_stream')"
check "the same advertisement comes back over the protocol itself" "true,true" \
  "$("$WORK/trace-client" caps "$OBS" | jq -r '[.supportsTraceGet,.supportsTraceStream]|map(tostring)|join(",")')"

note
note "== drive a dispatch and hold it open =="
SPEC='{"tag":"trace","spec":{"name":"trace","version":"1","nodes":[
 {"type":"fetch","executor":"http","attributes":{"schema":{"type":"object","properties":{"url":{"type":"string","default":"http://slow:8000/hold"},"method":{"type":"string","default":"GET"}}}}}
]}}'
REG=$(body POST /v1/templates "$SPEC")
TPL=$(printf '%s' "$REG" | jq -r '.template_id // empty')
check "template registered" yes "$([ -n "$TPL" ] && echo yes || echo no)"
[ -n "$TPL" ] || { note "register response: $REG"; note "EXPERIMENT FAIL"; exit 1; }
check "template deployed" 200 "$(code POST "/v1/templates/$TPL/deploy" '{}')"
IID=$(body POST /v1/instances "{\"template\":\"$TPL\",\"instance_key\":\"trace-1\",\"params\":{},\"target_agent\":\"trace-agent\"}" | jq -r '.instance_id // empty')
[ -n "$IID" ] || { note "instance create failed"; note "EXPERIMENT FAIL"; exit 1; }
code POST "/v1/instances/$IID/messages" '{"type":""}' >/dev/null

note "waiting for the executor's HTTP call to arrive and be held (blocks until it is)"
until [ "$(curl -sS -m 5 "http://127.0.0.1:$SLOW_PORT/status" | jq '.held')" -ge 1 ]; do sleep 0.3; done
note "waiting for the dispatch id to appear in the event feed (blocks until it does)"
until [ -n "$(body GET "/v1/events?instance_id=$IID&kind=work_started" | jq -r '.events[0].payload.dispatch_id // empty')" ]; do sleep 0.3; done
DISPATCH=$(body GET "/v1/events?instance_id=$IID&kind=work_started" | jq -r '.events[0].payload.dispatch_id')
note "dispatch in flight: $DISPATCH"

note
note "== stream the live trace while the dispatch is in flight =="
"$WORK/trace-client" stream "$OBS" "$DISPATCH" > "$WORK/stream.jsonl" 2>"$WORK/stream.err" &
STREAM_PID=$!
note "waiting for the first live trace event to arrive (blocks until it does)"
until [ -s "$WORK/stream.jsonl" ]; do sleep 0.2; done
FIRST=$(head -1 "$WORK/stream.jsonl")
check "the first streamed event is the executor's dispatch start" step_started "$(printf '%s' "$FIRST" | jq -r '.category')"
check "the dispatch had not finished when that event arrived" false \
  "$("$WORK/trace-client" get "$OBS" "$DISPATCH" | jq -r '.complete')"
check "no terminal event has been streamed yet" 0 \
  "$(grep -c 'step_completed' "$WORK/stream.jsonl" || true)"
check "the held request is still held" 1 "$(curl -sS -m 5 "http://127.0.0.1:$SLOW_PORT/status" | jq '.held')"

note
note "== release the endpoint; the same open stream carries the rest =="
curl -sS -m 5 "http://127.0.0.1:$SLOW_PORT/release" >/dev/null
note "waiting for the stream to carry the completion sentinel (blocks until it does)"
until grep -q 'trace_complete' "$WORK/stream.jsonl"; do sleep 0.2; done
wait "$STREAM_PID" 2>/dev/null
STREAM_PID=""
note "streamed categories in order: $(jq -r '.category' "$WORK/stream.jsonl" | paste -sd, -)"
check "the stream carried start then completion then the sentinel" "step_started,step_completed,trace_complete" \
  "$(jq -r '.category' "$WORK/stream.jsonl" | paste -sd, -)"

note
note "== fetch the finished record after the fact =="
TRACE=$("$WORK/trace-client" get "$OBS" "$DISPATCH")
check "the fetched trace is for the dispatch the feed named" "$DISPATCH" "$(printf '%s' "$TRACE" | jq -r '.dispatchId')"
check "the fetched trace is marked complete" true "$(printf '%s' "$TRACE" | jq -r '.complete')"
check "the fetched trace was not evicted" false "$(printf '%s' "$TRACE" | jq -r '.evicted')"
check "the fetched record carries both events the stream carried" "step_started,step_completed" \
  "$(printf '%s' "$TRACE" | jq -r '[.events[].category]|join(",")')"
check "every record is structured, not a log line" 0 \
  "$(printf '%s' "$TRACE" | jq '[.events[]|select((.eventId|length)==0 or (.timestamp|length)==0 or (.severity|length)==0 or (.category|length)==0 or (.message|length)==0)]|length')"
check "the records carry machine-readable attributes" yes \
  "$(printf '%s' "$TRACE" | jq -r 'if ([.events[]|select(.attributes.step_id!=null)]|length)>0 then "yes" else "no" end')"
check "the child event names its parent" yes \
  "$(printf '%s' "$TRACE" | jq -r 'if ([.events[]|select(.parentEventId!="")]|length)>0 then "yes" else "no" end')"
check "an unknown dispatch reads back as evicted rather than erroring" true \
  "$("$WORK/trace-client" get "$OBS" 00000000-0000-0000-0000-000000000000 | jq -r '.evicted')"

note
note "the record as a dashboard would render it:"
printf '%s' "$TRACE" | jq -r '.events[]|"    \(.timestamp)  \(.severity)  \(.category)  \(.message)"'

note
if [ "$fail" = 0 ]; then note "EXPERIMENT PASS"; else note "EXPERIMENT FAIL"; fi
exit "$fail"
