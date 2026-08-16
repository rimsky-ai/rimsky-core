#!/usr/bin/env bash
# Experiment: assumption-park-controls-on-every-executor
#
# Parked is a runtime-wide node state, so a template author testing
# park-and-resume expects `probe_park` and `park_resume_at` to work wherever a
# node runs. This run declares the same park attributes on a node per bundled
# executor -- all four started with RIMSKY_EXECUTOR_STUB_MODE=1, plus one
# http-node in its ordinary mode -- and records which dispatches actually park
# and at what resume time.
set -uo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
: "${RIMSKY_IMAGE_TAG:?export RIMSKY_IMAGE_TAG=src-<tree hash> first}"
TAG="$RIMSKY_IMAGE_TAG"
WORK="$(mktemp -d)"
RESUME_AT="2031-03-04T05:06:07Z"

NET=exp-assumption-park-net
STACK=exp-assumption-park-stack
HTTPNODE=exp-assumption-park-httpnode
HTTPLIVE=exp-assumption-park-httplive
VHTTP=exp-assumption-park-vhttp
VSHAPES=exp-assumption-park-vshapes
CLAUDE=exp-assumption-park-claude

fails=0
ok()  { printf 'PASS  %s\n' "$*"; }
bad() { printf 'FAIL  %s\n' "$*"; fails=$((fails+1)); }
check() { if [ "$2" = "$3" ]; then ok "$1 [$3]"; else bad "$1 expected [$2] got [$3]"; fi; }

free_port() { python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()'; }
uid() { python3 -c 'import uuid;print(uuid.uuid4().hex)'; }

cleanup() {
  docker rm -f "$STACK" "$HTTPNODE" "$HTTPLIVE" "$VHTTP" "$VSHAPES" "$CLAUDE" >/dev/null 2>&1
  docker network rm "$NET" >/dev/null 2>&1
  rm -rf "$WORK"
}
trap cleanup EXIT

cat > "$WORK/rimsky.yml" <<'YAML'
persistence:
  driver: sqlite
  sqlite:
    path: /var/lib/rimsky/state.db
named_locks: {}
claim_producers: {}
executors:
  "http-node":
    transport: grpc
    endpoint: "httpnode:9091"
    protocols: ["executor"]
    observability_endpoint: "httpnode:9091"
  "http-node-live":
    transport: grpc
    endpoint: "httplive:9091"
    protocols: ["executor"]
    observability_endpoint: "httplive:9091"
  "verifier-http":
    transport: grpc
    endpoint: "vhttp:9096"
    protocols: ["executor"]
    observability_endpoint: "vhttp:9096"
  "verifier-shape-checks":
    transport: grpc
    endpoint: "vshapes:9095"
    protocols: ["executor"]
    observability_endpoint: "vshapes:9095"
  "claude-agent":
    transport: grpc
    endpoint: "claude:9090"
    protocols: ["executor"]
    observability_endpoint: "claude:9090"
YAML

PORT=$(free_port); BASE="http://127.0.0.1:$PORT"
docker rm -f "$STACK" "$HTTPNODE" "$HTTPLIVE" "$VHTTP" "$VSHAPES" "$CLAUDE" >/dev/null 2>&1
docker network rm "$NET" >/dev/null 2>&1
docker network create "$NET" >/dev/null || exit 1
docker run -d --name "$HTTPNODE" --network "$NET" --network-alias httpnode -e RIMSKY_EXECUTOR_STUB_MODE=1 "rimsky-executor-http-node:$TAG" >/dev/null || exit 1
docker run -d --name "$HTTPLIVE" --network "$NET" --network-alias httplive "rimsky-executor-http-node:$TAG" >/dev/null || exit 1
docker run -d --name "$VHTTP" --network "$NET" --network-alias vhttp -e RIMSKY_EXECUTOR_STUB_MODE=1 -e RIMSKY_EXECUTOR_PORT_GRPC=9096 "rimsky-executor-verifier-http:$TAG" >/dev/null || exit 1
docker run -d --name "$VSHAPES" --network "$NET" --network-alias vshapes -e RIMSKY_EXECUTOR_STUB_MODE=1 -e RIMSKY_EXECUTOR_PORT_GRPC=9095 "rimsky-executor-verifier-shape-checks:$TAG" >/dev/null || exit 1
docker run -d --name "$CLAUDE" --network "$NET" --network-alias claude -e RIMSKY_EXECUTOR_STUB_MODE=1 "rimsky-executor-claude-agent:$TAG" >/dev/null || exit 1
docker run -d --name "$STACK" --network "$NET" --network-alias rimsky \
  -e RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_HOST=rimsky -p "127.0.0.1:$PORT:8080" \
  -v "$WORK/rimsky.yml:/etc/rimsky/rimsky.yml:ro" "rimsky-all-in-one:$TAG" >/dev/null || exit 1

get()  { curl -s "$BASE$1"; }
post() { curl -s -X POST -H 'content-type: application/json' -H "Idempotency-Key: $(uid)" -d "$2" "$BASE$1"; }
until [ "$(curl -s -o /dev/null -w '%{http_code}' "$BASE/v1/health")" = 200 ]; do sleep 0.5; done

# outcome <template-json> <key> -> "registered|park_resume_at|terminal_kind|error_class"
outcome() {
  tpl=$(post /v1/templates "$1" | jq -r '.template_id // empty')
  [ -n "$tpl" ] || { echo "no|||"; return; }
  post "/v1/templates/$tpl/deploy" '{}' >/dev/null
  iid=$(post /v1/instances "$(printf '{"template":"%s","instance_key":"%s","target_agent":"audit-agent","params":{}}' "$tpl" "$2")" | jq -r '.instance_id // empty')
  post "/v1/instances/$iid/messages" '{}' >/dev/null
  while :; do
    settled=$(get "/v1/events?instance_id=$iid" | jq -r '[.events[]? | select(.kind=="transient/park" or (.kind|startswith("terminal/error")) or (.kind=="work_completed"))] | length')
    [ "$settled" -gt 0 ] && break
    sleep 0.4
  done
  parked=$(get "/v1/admin/diagnostics/parked-nodes" | jq -r --arg i "$iid" '[.parked_nodes[]? | select(.instance_id==$i) | .resume_at] | first // ""')
  kind=$(get "/v1/events?instance_id=$iid" | jq -r '[.events[]? | select((.kind|startswith("terminal/")) or .kind=="transient/park") | .kind] | first // ""')
  eclass=$(get "/v1/events?instance_id=$iid" | jq -r '[.events[]? | .payload.error_class? // empty] | first // ""')
  printf 'yes|%s|%s|%s\n' "$parked" "$kind" "$eclass"
}

PARK_PROPS="\"probe_park\":{\"type\":\"boolean\",\"default\":true},\"park_resume_at\":{\"type\":\"string\",\"default\":\"$RESUME_AT\"}"

echo "--- every executor's schema accepts the park attributes at registration"
echo "--- http-node parks at the author's resume time"
r=$(outcome "{\"tag\":\"p-httpnode\",\"spec\":{\"name\":\"p-httpnode\",\"version\":\"1\",\"nodes\":[{\"type\":\"w\",\"executor\":\"http-node\",\"attributes\":{\"schema\":{\"type\":\"object\",\"properties\":{\"url\":{\"type\":\"string\",\"default\":\"http://nowhere.invalid/x\"},$PARK_PROPS}}}}]}}" k-p-httpnode)
printf '    %s\n' "$r"
check "the template registered" yes "$(printf '%s' "$r" | cut -d'|' -f1)"
check "the node is parked at the declared resume time" "$RESUME_AT" "$(printf '%s' "$r" | cut -d'|' -f2)"

echo "--- claude-agent parks at the author's resume time"
r=$(outcome "{\"tag\":\"p-claude\",\"spec\":{\"name\":\"p-claude\",\"version\":\"1\",\"nodes\":[{\"type\":\"w\",\"executor\":\"claude-agent\",\"attributes\":{\"schema\":{\"type\":\"object\",\"properties\":{\"system_prompt\":{\"type\":\"string\",\"default\":\"s\"},\"user_prompt\":{\"type\":\"string\",\"default\":\"hi\"},\"cli\":{\"type\":\"object\",\"default\":{}},$PARK_PROPS}}}}]}}" k-p-claude)
printf '    %s\n' "$r"
check "the template registered" yes "$(printf '%s' "$r" | cut -d'|' -f1)"
check "the node is parked at the declared resume time" "$RESUME_AT" "$(printf '%s' "$r" | cut -d'|' -f2)"

echo "--- verifier-http takes the attributes and completes anyway"
r=$(outcome "{\"tag\":\"p-vhttp\",\"spec\":{\"name\":\"p-vhttp\",\"version\":\"1\",\"nodes\":[{\"type\":\"w\",\"executor\":\"verifier-http\",\"attributes\":{\"schema\":{\"type\":\"object\",\"properties\":{\"url\":{\"type\":\"string\",\"default\":\"http://nowhere.invalid/x\"},$PARK_PROPS}}}}]}}" k-p-vhttp)
printf '    %s\n' "$r"
check "the template registered" yes "$(printf '%s' "$r" | cut -d'|' -f1)"
check "nothing parked" "" "$(printf '%s' "$r" | cut -d'|' -f2)"

echo "--- verifier-shape-checks takes the attributes and errors"
r=$(outcome "{\"tag\":\"p-vshapes\",\"spec\":{\"name\":\"p-vshapes\",\"version\":\"1\",\"nodes\":[{\"type\":\"w\",\"executor\":\"verifier-shape-checks\",\"attributes\":{\"schema\":{\"type\":\"object\",\"properties\":{\"checks\":{\"type\":\"array\",\"default\":[{\"kind\":\"row_count\"}]},$PARK_PROPS}}}}]}}" k-p-vshapes)
printf '    %s\n' "$r"
check "the template registered" yes "$(printf '%s' "$r" | cut -d'|' -f1)"
check "nothing parked" "" "$(printf '%s' "$r" | cut -d'|' -f2)"
check "the dispatch errored on the real check path" "verifier/attribute_invalid" "$(printf '%s' "$r" | cut -d'|' -f4)"

echo "--- the park controls do nothing without the operator's env flag"
r=$(outcome "{\"tag\":\"p-httplive\",\"spec\":{\"name\":\"p-httplive\",\"version\":\"1\",\"nodes\":[{\"type\":\"w\",\"executor\":\"http-node-live\",\"attributes\":{\"schema\":{\"type\":\"object\",\"properties\":{\"url\":{\"type\":\"string\",\"default\":\"http://nowhere.invalid/x\"},$PARK_PROPS}}}}]}}" k-p-httplive)
printf '    %s\n' "$r"
check "nothing parked" "" "$(printf '%s' "$r" | cut -d'|' -f2)"
check "the ordinary http-node made the real request instead" "http/network_error" "$(printf '%s' "$r" | cut -d'|' -f4)"

echo
if [ "$fails" -eq 0 ]; then echo "EXPERIMENT PASS"; else echo "EXPERIMENT FAIL ($fails)"; fi
exit "$fails"
