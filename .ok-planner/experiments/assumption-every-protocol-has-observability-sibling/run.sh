#!/usr/bin/env bash
# Experiment: assumption-every-protocol-has-observability-sibling
#
# An operator building a dashboard sees ExecutorObservability and
# ClaimProducerObservability among the shipped protocols, and sees the config
# accept `observability_endpoint` under executors, claim_producers, publishers,
# validators and data_processors alike. This run asks what the product actually
# offers per protocol: it wires a stack whose executor, claim producer and
# publisher each declare an observability endpoint, then reads the control
# API's observability surface and dials the observability sibling of each
# protocol directly.
#
# Instrument: the control API and the shipped gRPC method names only.
set -uo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../../.." && pwd)"
: "${RIMSKY_IMAGE_TAG:?export RIMSKY_IMAGE_TAG=src-<tree hash> first}"
TAG="$RIMSKY_IMAGE_TAG"
WORK="$(mktemp -d)"

NET=exp-assumption-obssib-net
STACK=exp-assumption-obssib-stack
EXECUTOR=exp-assumption-obssib-executor
PRODUCER=exp-assumption-obssib-producer
PUBLISHER=exp-assumption-obssib-publisher
SHAPES=exp-assumption-obssib-shapes

fails=0
ok()  { printf 'PASS  %s\n' "$*"; }
bad() { printf 'FAIL  %s\n' "$*"; fails=$((fails+1)); }
check() { if [ "$2" = "$3" ]; then ok "$1 [$3]"; else bad "$1 expected [$2] got [$3]"; fi; }

free_port() { python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()'; }

cleanup() {
  docker rm -f "$STACK" "$EXECUTOR" "$PRODUCER" "$PUBLISHER" "$SHAPES" >/dev/null 2>&1
  docker network rm "$NET" >/dev/null 2>&1
  rm -rf "$WORK"
}
trap cleanup EXIT

cp -r "$HERE/probe" "$WORK/probe"
sed "s|RIMSKY_PROTOCOLS_PATH|$ROOT/lib/protocols|" "$WORK/probe/go.mod.tmpl" > "$WORK/probe/go.mod"
rm "$WORK/probe/go.mod.tmpl"
(cd "$WORK/probe" && GOFLAGS=-mod=mod go build -o "$WORK/probe-bin" .) || { echo "probe build failed"; exit 1; }

mkdir -p "$WORK/data"
cat > "$WORK/fs.yml" <<'YAML'
root: /workspace/data
host: 0.0.0.0
grpc_port: 9100
http_port: 9110
enable_lifecycle: true
sweep_interval_seconds: 60
YAML

cat > "$WORK/rimsky.yml" <<'YAML'
persistence:
  driver: sqlite
  sqlite:
    path: /var/lib/rimsky/state.db
named_locks: {}
claim_producers:
  "files":
    endpoint: "producer:9100"
    protocols: ["claim_producer"]
    write_semantics_allowed: ["sync"]
    observability_endpoint: "producer:9100"
executors:
  "http":
    transport: grpc
    endpoint: "executor:9091"
    protocols: ["executor"]
    observability_endpoint: "executor:9091"
publishers:
  "tick":
    endpoint: "publisher:9081"
    protocols: ["publisher"]
    observability_endpoint: "publisher:9081"
validators:
  "shapes":
    endpoint: "shapes:9095"
    protocols: ["validation"]
    observability_endpoint: "shapes:9095"
YAML

PORT=$(free_port); P_PUB=$(free_port); P_PROD=$(free_port); P_SHAPES=$(free_port)
BASE="http://127.0.0.1:$PORT"

docker rm -f "$STACK" "$EXECUTOR" "$PRODUCER" "$PUBLISHER" "$SHAPES" >/dev/null 2>&1
docker network rm "$NET" >/dev/null 2>&1
docker network create "$NET" >/dev/null || exit 1

docker run -d --name "$EXECUTOR" --network "$NET" --network-alias executor \
  "rimsky-executor-http-node:$TAG" >/dev/null || exit 1
docker run -d --name "$PRODUCER" --network "$NET" --network-alias producer \
  -e RIMSKY_CLAIM_PRODUCER_FILESYSTEM_CONFIG=/etc/rimsky/fs.yml \
  -v "$WORK/fs.yml:/etc/rimsky/fs.yml:ro" -v "$WORK/data:/workspace/data" \
  -p "127.0.0.1:$P_PROD:9100" "rimsky-claim-producer-filesystem:$TAG" >/dev/null || exit 1
docker run -d --name "$PUBLISHER" --network "$NET" --network-alias publisher \
  -e RIMSKY_SENSOR_CRON_PORT=9081 -e RIMSKY_CONTROL_API_URL=http://rimsky:8080 \
  -p "127.0.0.1:$P_PUB:9081" "rimsky-sensor-cron:$TAG" >/dev/null || exit 1
docker run -d --name "$SHAPES" --network "$NET" --network-alias shapes \
  -e RIMSKY_EXECUTOR_PORT_GRPC=9095 -p "127.0.0.1:$P_SHAPES:9095" \
  "rimsky-executor-verifier-shape-checks:$TAG" >/dev/null || exit 1
docker run -d --name "$STACK" --network "$NET" --network-alias rimsky -p "127.0.0.1:$PORT:8080" \
  -v "$WORK/rimsky.yml:/etc/rimsky/rimsky.yml:ro" "rimsky-all-in-one:$TAG" >/dev/null || exit 1

code() { curl -s -o /dev/null -w '%{http_code}' -X "$1" "$BASE$2"; }
body() { curl -s -X "$1" "$BASE$2"; }

until [ "$(code GET /v1/health)" = 200 ]; do sleep 0.5; done

echo "--- the stack accepts an observability_endpoint on every peer class"
check "the stack boots with observability_endpoint set on executor, producer, publisher and validator" 200 "$(code GET /v1/health)"

echo "--- the two protocols with an observability sibling are readable"
until [ "$(body GET /v1/observability/executors/http | jq -r '.peer.reachability_status')" = reachable ]; do sleep 0.5; done
check "the control API reads the executor's observability capabilities" true \
  "$(body GET /v1/observability/executors/http | jq -r '.peer.observability_capabilities.supports_trace_get')"
check "the control API reads the claim producer's observability capabilities" true \
  "$(body GET /v1/observability/claim-producers/files | jq -r '.peer.observability_capabilities.supports_claim_get')"

echo "--- the same read has no publisher, subscriber, validator or data-processor form"
for path in publishers subscribers lifecycle-subscribers validators data-processors; do
  check "GET /v1/observability/$path is not a route" 404 "$(code GET "/v1/observability/$path")"
done
check "the publisher this stack was told to observe is named nowhere in the observability surface" "" \
  "$(body GET /v1/observability/system/summary | grep -o 'tick' | head -1)"

echo "--- the observability sibling protocols themselves"
sibling() {
  out="$("$WORK/probe-bin" -addr "127.0.0.1:$2" -method "/rimsky.v1.$1/Capabilities")"
  printf '    %s\n' "$out"
  case "$out" in
    "Unimplemented"*"unknown service rimsky.v1.$1"*) ok "$1 is not a protocol this deployment serves";;
    *) bad "$1 probe did not read as an absent service: $out";;
  esac
}
present() {
  out="$("$WORK/probe-bin" -addr "127.0.0.1:$2" -method "/rimsky.v1.$1/Capabilities")"
  printf '    %s\n' "$out"
  case "$out" in OK*) ok "$1 answers at the endpoint the config calls its observability endpoint";;
    *) bad "expected $1 to answer, got: $out";; esac
}
present ClaimProducerObservability "$P_PROD"
sibling PublisherObservability "$P_PUB"
sibling LifecycleSubscriberObservability "$P_PROD"
sibling ValidationObservability "$P_SHAPES"
sibling DataProcessingObservability "$P_PROD"

echo
if [ "$fails" -eq 0 ]; then echo "EXPERIMENT PASS"; else echo "EXPERIMENT FAIL ($fails)"; fi
exit "$fails"
