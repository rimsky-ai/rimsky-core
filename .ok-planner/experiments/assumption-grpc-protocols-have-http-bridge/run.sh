#!/usr/bin/env bash
# Experiment: assumption-grpc-protocols-have-http-bridge
#
# A service author whose language handles gRPC badly wants to know which
# protocols they can implement over HTTP+JSON instead. The config carries an
# `executors.<name>.transport` selector and the bundled claim producers publish
# an HTTP bridge port, so the run asks the product protocol by protocol: it
# drives the claim-producer bridge with the conformance CLI, calls the bundled
# executor's bridge directly, wires a stack that dispatches to that executor
# over `transport: http` on a non-default port, and asks the CLI for an HTTP
# transport on every remaining protocol.
set -uo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../../.." && pwd)"
CLI="$ROOT/bin/rimsky"
: "${RIMSKY_IMAGE_TAG:?export RIMSKY_IMAGE_TAG=src-<tree hash> first}"
TAG="$RIMSKY_IMAGE_TAG"
WORK="$(mktemp -d)"

NET=exp-assumption-bridge-net
STACK=exp-assumption-bridge-stack
STACK_CP=exp-assumption-bridge-stack-cp
STACK_OBS=exp-assumption-bridge-stack-obs
EXECUTOR=exp-assumption-bridge-executor
PRODUCER=exp-assumption-bridge-producer

fails=0
ok()  { printf 'PASS  %s\n' "$*"; }
bad() { printf 'FAIL  %s\n' "$*"; fails=$((fails+1)); }
has() { case "$2" in *"$1"*) ok "$3";; *) bad "$3 (missing '$1')";; esac; }
check() { if [ "$2" = "$3" ]; then ok "$1 [$3]"; else bad "$1 expected [$2] got [$3]"; fi; }

free_port() { python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()'; }
uid() { python3 -c 'import uuid;print(uuid.uuid4().hex)'; }

cleanup() {
  docker rm -f "$STACK" "$STACK_CP" "$STACK_OBS" "$EXECUTOR" "$PRODUCER" >/dev/null 2>&1
  docker network rm "$NET" >/dev/null 2>&1
  rm -rf "$WORK"
}
trap cleanup EXIT

[ -x "$CLI" ] || { echo "build the CLI first: make cli"; exit 1; }

mkdir -p "$WORK/data"
cat > "$WORK/fs.yml" <<'YAML'
root: /workspace/data
host: 0.0.0.0
grpc_port: 9100
http_port: 9110
sweep_interval_seconds: 60
YAML

cat > "$WORK/rimsky.yml" <<'YAML'
persistence:
  driver: sqlite
  sqlite:
    path: /var/lib/rimsky/state.db
named_locks: {}
claim_producers: {}
executors:
  "bridge":
    transport: http
    endpoint: "http://executor:9099"
    protocols: ["executor"]
    observability_endpoint: "executor:9091"
YAML

sed 's|observability_endpoint: "executor:9091"|observability_endpoint: "http://executor:9099"|' \
  "$WORK/rimsky.yml" > "$WORK/rimsky-obs-http.yml"

cat > "$WORK/rimsky-cp-http.yml" <<'YAML'
persistence:
  driver: sqlite
  sqlite:
    path: /var/lib/rimsky/state.db
named_locks: {}
executors: {}
claim_producers:
  "files":
    endpoint: "http://producer:9110"
    protocols: ["claim_producer"]
    write_semantics_allowed: ["sync"]
YAML

PORT=$(free_port); PORT_CP=$(free_port); PORT_OBS=$(free_port)
P_HTTP=$(free_port); P_PROD_HTTP=$(free_port)
BASE="http://127.0.0.1:$PORT"

docker rm -f "$STACK" "$STACK_CP" "$STACK_OBS" "$EXECUTOR" "$PRODUCER" >/dev/null 2>&1
docker network rm "$NET" >/dev/null 2>&1
docker network create "$NET" >/dev/null || exit 1

docker run -d --name "$PRODUCER" --network "$NET" --network-alias producer \
  -e RIMSKY_CLAIM_PRODUCER_FILESYSTEM_CONFIG=/etc/rimsky/fs.yml \
  -v "$WORK/fs.yml:/etc/rimsky/fs.yml:ro" -v "$WORK/data:/workspace/data" \
  -p "127.0.0.1:$P_PROD_HTTP:9110" \
  "rimsky-claim-producer-filesystem:$TAG" >/dev/null || exit 1
docker run -d --name "$EXECUTOR" --network "$NET" --network-alias executor \
  -e RIMSKY_EXECUTOR_PORT_GRPC=9091 -e RIMSKY_EXECUTOR_PORT_HTTP=9099 \
  -e RIMSKY_EXECUTOR_STUB_MODE=1 -p "127.0.0.1:$P_HTTP:9099" \
  "rimsky-executor-http-node:$TAG" >/dev/null || exit 1
for p in "$P_PROD_HTTP" "$P_HTTP"; do until nc -z 127.0.0.1 "$p" >/dev/null 2>&1; do sleep 0.2; done; done

echo "--- the claim-producer protocol over its HTTP bridge"
out="$("$CLI" conformance claim-producer --transport http --endpoint "http://127.0.0.1:$P_PROD_HTTP" --timeout 30s 2>&1)"; rc=$?
printf '%s\n' "$out" | sed 's/^/    /'
check "the whole claim-producer suite passes over HTTP+JSON" 0 "$rc"
has "ok    Commit" "$out" "the terminal verbs run over the bridge"

echo "--- the observability sibling of that protocol has no bridge"
out="$("$CLI" conformance claim-producer --transport http --endpoint "http://127.0.0.1:$P_PROD_HTTP" --check-observability --timeout 30s 2>&1)"; rc=$?
printf '    %s\n' "$(printf '%s' "$out" | tail -1)"
has "ClaimProducerObservability has no HTTP+JSON bridge" "$out" "the CLI says ClaimProducerObservability has no bridge"
check "asking for it is a usage error" 2 "$rc"

echo "--- the executor protocol over its HTTP bridge, called directly"
curl -s -o "$WORK/exec.json" -w '%{http_code}' -X POST -H 'content-type: application/json' \
  -d '{"node_type":"n","attributes":{}}' "http://127.0.0.1:$P_HTTP/v1/Execute" > "$WORK/exec.code"
check "POST /v1/Execute answers" 200 "$(cat "$WORK/exec.code")"
has "errorClass" "$(cat "$WORK/exec.json")" "the bridge answers with a JSON Outcome"
check "the executor also bridges its observability capabilities" 200 \
  "$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$P_HTTP/observability/v1/capabilities")"

echo "--- rimsky dispatches over transport: http"
docker run -d --name "$STACK" --network "$NET" -p "127.0.0.1:$PORT:8080" \
  -v "$WORK/rimsky.yml:/etc/rimsky/rimsky.yml:ro" "rimsky-all-in-one:$TAG" >/dev/null || exit 1
code() {
  if [ $# -ge 3 ]; then
    curl -s -o /dev/null -w '%{http_code}' -X "$1" -H 'content-type: application/json' -d "$3" "$BASE$2"
  else
    curl -s -o /dev/null -w '%{http_code}' -X "$1" "$BASE$2"
  fi
}
body() {
  if [ $# -ge 3 ]; then
    curl -s -X "$1" -H 'content-type: application/json' -H "Idempotency-Key: $(uid)" -d "$3" "$BASE$2"
  else
    curl -s -X "$1" "$BASE$2"
  fi
}
until [ "$(code GET /v1/health)" = 200 ]; do sleep 0.5; done
SPEC='{"tag":"bridge","spec":{"name":"bridge","version":"1","nodes":[{"type":"worker","executor":"bridge","attributes":{"schema":{"type":"object","properties":{"stub_probe":{"type":"boolean","default":true}}}}}]}}'
TPL=$(body POST /v1/templates "$SPEC" | jq -r '.template_id // empty')
check "the stack accepted an executor declared transport: http" yes "$([ -n "$TPL" ] && echo yes || echo no)"
check "template deployed" 200 "$(code POST "/v1/templates/$TPL/deploy" '{}')"
IID=$(body POST /v1/instances "$(printf '{"template":"%s","instance_key":"ck-bridge","target_agent":"audit-agent","params":{}}' "$TPL")" | jq -r '.instance_id // empty')
check "instance created" yes "$([ -n "$IID" ] && echo yes || echo no)"
body POST "/v1/instances/$IID/messages" '{}' >/dev/null
while :; do
  S=$(body GET "/v1/observability/nodes/$IID/worker" | jq -r '.run_summary as $r | if $r == null then "in-flight" elif $r.failed_count > 0 then "failed" elif $r.fresh_count > 0 and $r.active_count == 0 and $r.pending_count == 0 then "fresh" else "in-flight" end')
  [ "$S" = in-flight ] || break
  sleep 0.5
done
check "the node settled through the executor's HTTP bridge on port 9099" fresh "$S"

echo "--- rimsky reads observability over gRPC only"
docker run -d --name "$STACK_OBS" --network "$NET" -p "127.0.0.1:$PORT_OBS:8080" \
  -v "$WORK/rimsky-obs-http.yml:/etc/rimsky/rimsky.yml:ro" "rimsky-all-in-one:$TAG" >/dev/null || exit 1
OBS_BASE="http://127.0.0.1:$PORT_OBS"
until [ "$(curl -s -o /dev/null -w '%{http_code}' "$OBS_BASE/v1/health")" = 200 ]; do sleep 0.5; done
OBS_LOG="$(docker logs "$STACK_OBS" 2>&1 | grep observability.handshake.executor.unreachable | head -1)"
printf '    %s\n' "$OBS_LOG"
has "http2: frame too large" "$OBS_LOG" "pointing observability_endpoint at the executor's HTTP bridge is a failed gRPC dial"
REJECT="$(curl -s -X POST -H 'content-type: application/json' -d "$SPEC" "$OBS_BASE/v1/templates")"
has "expected_attributes_schema is not visible" "$REJECT" "so the executor's schema never becomes visible"

echo "--- rimsky reaches claim producers over gRPC only"
docker run -d --name "$STACK_CP" --network "$NET" -p "127.0.0.1:$PORT_CP:8080" \
  -v "$WORK/rimsky-cp-http.yml:/etc/rimsky/rimsky.yml:ro" "rimsky-all-in-one:$TAG" >/dev/null || exit 1
while [ "$(docker inspect -f '{{.State.Status}}' "$STACK_CP")" = running ]; do sleep 0.5; done
CP_LOG="$(docker logs "$STACK_CP" 2>&1 | tail -3)"
printf '%s\n' "$CP_LOG" | sed 's/^/    /'
check "a claim producer pointed at its HTTP bridge stops the stack" exited "$(docker inspect -f '{{.State.Status}}' "$STACK_CP")"
has "dialRemoteClaimProducers" "$CP_LOG" "the failure is rimsky's gRPC dial of the claim producer"

echo "--- the protocols the CLI will not speak over HTTP"
for sub in executor validation data-processing lifecycle-subscriber; do
  out="$("$CLI" conformance "$sub" --transport http --endpoint http://127.0.0.1:1 2>&1)"; rc=$?
  printf '    %s\n' "$out"
  has 'not supported; use grpc' "$out" "'$sub --transport http' is refused"
  check "'$sub --transport http' is a usage error" 2 "$rc"
done
out="$("$CLI" conformance publisher --kind cron --transport http --endpoint http://127.0.0.1:1 2>&1)"; rc=$?
printf '    %s\n' "$out"
has 'not supported; use grpc' "$out" "'publisher --transport http' is refused"
check "'publisher --transport http' is a usage error" 2 "$rc"

echo
if [ "$fails" -eq 0 ]; then echo "EXPERIMENT PASS"; else echo "EXPERIMENT FAIL ($fails)"; fi
exit "$fails"
