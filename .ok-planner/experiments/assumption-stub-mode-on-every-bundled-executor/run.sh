#!/usr/bin/env bash
# Experiment: assumption-stub-mode-on-every-bundled-executor
#
# A template author who wants to exercise a whole graph offline reaches for
# `stub_response`. This run puts all four bundled executors behind one stack --
# each started with RIMSKY_EXECUTOR_STUB_MODE=1, the most favourable condition
# for the attribute -- plus a fifth container running http-node in its ordinary
# mode, and drives one node per executor with `stub_response` set. It records
# what each dispatch settled with.
set -uo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
: "${RIMSKY_IMAGE_TAG:?export RIMSKY_IMAGE_TAG=src-<tree hash> first}"
TAG="$RIMSKY_IMAGE_TAG"
WORK="$(mktemp -d)"

NET=exp-assumption-stubattr-net
STACK=exp-assumption-stubattr-stack
HTTPNODE=exp-assumption-stubattr-httpnode
HTTPLIVE=exp-assumption-stubattr-httplive
VHTTP=exp-assumption-stubattr-vhttp
VSHAPES=exp-assumption-stubattr-vshapes
CLAUDE=exp-assumption-stubattr-claude

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

# settle <template-json> <key> -> prints "state|delta|error_class"
settle() {
  tpl=$(post /v1/templates "$1" | jq -r '.template_id // empty')
  [ -n "$tpl" ] || { echo "REGISTRATION-FAILED||"; return; }
  post "/v1/templates/$tpl/deploy" '{}' >/dev/null
  iid=$(post /v1/instances "$(printf '{"template":"%s","instance_key":"%s","target_agent":"audit-agent","params":{}}' "$tpl" "$2")" | jq -r '.instance_id // empty')
  post "/v1/instances/$iid/messages" '{}' >/dev/null
  while :; do
    s=$(get "/v1/observability/nodes/$iid/w" | jq -r '.run_summary as $r | if $r == null then "in-flight" elif $r.failed_count>0 then "failed" elif $r.fresh_count>0 and $r.active_count==0 and $r.pending_count==0 then "fresh" else "in-flight" end')
    [ "$s" = in-flight ] || break
    sleep 0.4
  done
  ev=$(get "/v1/events?instance_id=$iid" | jq -c '[.events[]? | select(.kind|startswith("terminal/")) | {d:(.payload.attributes_delta//{}), e:(.payload.error_class//"")}] | map(select((.d|length)>0 or .e!="")) | first // {d:{},e:""}')
  printf '%s|%s|%s\n' "$s" "$(printf '%s' "$ev" | jq -c '.d')" "$(printf '%s' "$ev" | jq -r '.e')"
}

tpl_http() { cat <<JSON
{"tag":"$1","spec":{"name":"$1","version":"1","nodes":[{"type":"w","executor":"$2","attributes":{"schema":{"type":"object","properties":{"url":{"type":"string","default":"http://nowhere.invalid/x"},"stub_response":{"type":"object","default":{"canned":true}}}}}}]}}
JSON
}

echo "--- http-node honours the author's stub_response"
r=$(settle "$(tpl_http s-httpnode http-node)" k-httpnode); printf '    %s\n' "$r"
check "the node settles from the canned response" "fresh" "${r%%|*}"
check "the delta is the author's own object" '{"canned":true}' "$(printf '%s' "$r" | cut -d'|' -f2)"

echo "--- verifier-http accepts the attribute and ignores it"
r=$(settle "$(tpl_http s-vhttp verifier-http)" k-vhttp); printf '    %s\n' "$r"
check "the node settles" "fresh" "${r%%|*}"
check "the delta is the executor's canned stub marker, not the author's object" '{"stub":true}' "$(printf '%s' "$r" | cut -d'|' -f2)"

echo "--- verifier-shape-checks does not stub at all"
r=$(settle '{"tag":"s-vshapes","spec":{"name":"s-vshapes","version":"1","nodes":[{"type":"w","executor":"verifier-shape-checks","attributes":{"schema":{"type":"object","properties":{"checks":{"type":"array","default":[{"kind":"row_count"}]},"stub_response":{"type":"object","default":{"canned":true}}}}}}]}}' k-vshapes)
printf '    %s\n' "$r"
check "the node fails" "failed" "${r%%|*}"
check "it fails asking for the real data the check needs" "verifier/attribute_invalid" "$(printf '%s' "$r" | cut -d'|' -f3)"

CLAUDE_PROPS='"system_prompt":{"type":"string","default":"s"},"user_prompt":{"type":"string","default":"hi"},"cli":{"type":"object","default":{}},"stub_response":{"type":"object","default":{"answer":"x"}}'
echo "--- claude-agent honours stub_response only under stub_probe"
r=$(settle "{\"tag\":\"s-claude-probe\",\"spec\":{\"name\":\"s-claude-probe\",\"version\":\"1\",\"nodes\":[{\"type\":\"w\",\"executor\":\"claude-agent\",\"attributes\":{\"schema\":{\"type\":\"object\",\"properties\":{$CLAUDE_PROPS,\"stub_probe\":{\"type\":\"boolean\",\"default\":true}}}}}]}}" k-claude-probe)
printf '    %s\n' "$r"
check "with stub_probe the node settles" "fresh" "${r%%|*}"
check "with stub_probe the delta carries the author's key" "x" "$(printf '%s' "$r" | cut -d'|' -f2 | jq -r '.answer // "missing"')"
r=$(settle "{\"tag\":\"s-claude-plain\",\"spec\":{\"name\":\"s-claude-plain\",\"version\":\"1\",\"nodes\":[{\"type\":\"w\",\"executor\":\"claude-agent\",\"attributes\":{\"schema\":{\"type\":\"object\",\"properties\":{$CLAUDE_PROPS}}}}]}}" k-claude-plain)
printf '    %s\n' "$r"
check "without stub_probe the author's key is dropped" "missing" "$(printf '%s' "$r" | cut -d'|' -f2 | jq -r '.answer // "missing"')"
check "without stub_probe the canned stub marker comes back instead" "true" "$(printf '%s' "$r" | cut -d'|' -f2 | jq -r '.stub // "missing"')"

echo "--- the attribute does nothing without the operator's env flag"
r=$(settle "$(tpl_http s-httplive http-node-live)" k-httplive); printf '    %s\n' "$r"
check "the same node against an ordinary http-node fails" "failed" "${r%%|*}"
check "it made the real request the author meant to stub out" "http/network_error" "$(printf '%s' "$r" | cut -d'|' -f3)"

echo
if [ "$fails" -eq 0 ]; then echo "EXPERIMENT PASS"; else echo "EXPERIMENT FAIL ($fails)"; fi
exit "$fails"
