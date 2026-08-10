#!/usr/bin/env bash
# Experiment: named-lock-metric
#
# Saturates a named lock (limit 1) with three slow nodes while a fourth node
# takes a producer claim from the bundled filesystem claim producer, then
# scrapes the platform metrics endpoint and reads both off the same series:
#
#   named-lock acquisitions are in the metrics
#   producer-claim acquisitions are in the same metric family, alongside them
#   contention shows up as its own labelled counter, so saturation is graphable
#
# The executor is peer/, reused from the permissive-peer-build experiment, so
# each lock holder can be made slow enough to make the others queue.

set -u
cd "$(dirname "$0")"
ROOT=$(cd ../../.. && pwd)
: "${RIMSKY_IMAGE_TAG:?export RIMSKY_IMAGE_TAG=src-<tree hash> first}"
NET=exp-lockmetric-net
STACK=exp-lockmetric-stack
PEER=exp-lockmetric-peer
PROD=exp-lockmetric-producer
PORT=${PORT:-19314}
METRICS_PORT=${METRICS_PORT:-19324}
WORK=$(mktemp -d)
BASE="http://127.0.0.1:$PORT"
METRICS="http://127.0.0.1:$METRICS_PORT/metrics"

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
    -H "Idempotency-Key: nl-$RANDOM$RANDOM" ${b:+-d "$b"} "$BASE$p"; }
code() { req "$@" | tail -1; }
body() { req "$@" | sed '$d'; }
series() { curl -sS -m 20 "$METRICS" | grep "^rimsky_claim_acquisitions_total{$1}" | awk '{print $2}'; }

note "== build the executor and bring up the stack, the lock and a producer =="
cp -r peer "$WORK/peer"
sed "s|RIMSKY_PROTOCOLS_PATH|$ROOT/lib/protocols|" "$WORK/peer/go.mod.tmpl" > "$WORK/peer/go.mod"
rm "$WORK/peer/go.mod.tmpl"
ARCH=$(docker info --format '{{.Architecture}}' | sed 's/aarch64/arm64/;s/x86_64/amd64/')
( cd "$WORK/peer" && GOOS=linux GOARCH="$ARCH" CGO_ENABLED=0 GOFLAGS=-mod=mod GOPROXY=off GOSUMDB=off go build -o "$WORK/peer-linux" . ) || { note "peer build failed"; exit 1; }

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
named_locks:
  gate:
    limit: 1
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
docker run -d --name "$STACK" --network "$NET" -p "$PORT:8080" -p "$METRICS_PORT:9465" \
  -e RIMSKY_METRICS_HOST=0.0.0.0 -e RIMSKY_METRICS_PORT=9464 \
  -v "$WORK/rimsky.yml:/etc/rimsky/rimsky.yml:ro" "rimsky-all-in-one:$RIMSKY_IMAGE_TAG" >/dev/null || exit 1
until [ "$(code GET /v1/health)" = 200 ]; do sleep 0.5; done
until [ "$(body GET /v1/observability/executors/third-party | jq -r '.peer.reachability_status')" = reachable ]; do sleep 0.5; done
until [ "$(body GET /v1/observability/claim-producers | jq -r '.claim_producers[0].reachability_status')" = reachable ]; do sleep 0.5; done
until curl -sf -m 5 "$METRICS" >/dev/null; do sleep 0.5; done
check "the platform metrics endpoint is serving" yes "$(curl -sS -m 10 "$METRICS" | grep -q '^rimsky_' && echo yes || echo no)"

note
note "== three nodes contend for one named lock while a fourth takes a claim =="
slow() { printf '{"type":"%s","executor":"third-party","locks":[{"name":"gate"}],"attributes":{"schema":{"type":"object","properties":{"outcome":{"type":"string","default":"ok"},"sleep_ms":{"type":"integer","default":1500}}}}}' "$1"; }
SPEC=$(printf '{"tag":"lockmetric","spec":{"name":"lockmetric","version":"1","nodes":[%s,%s,%s,%s]}}' \
  "$(slow slow_a)" "$(slow slow_b)" "$(slow slow_c)" \
  '{"type":"claimer","executor":"third-party","claim_producers":[{"name":"files","selector":"notes/a.txt","intent":"rw"}],"error_types":{"acquire/unavailable":{"action":"retry"}},"attributes":{"schema":{"type":"object","properties":{"outcome":{"type":"string","default":"ok"},"sleep_ms":{"type":"integer","default":0}}}}}')
REG=$(body POST /v1/templates "$SPEC")
TPL=$(printf '%s' "$REG" | jq -r '.template_id // empty')
check "template registered" yes "$([ -n "$TPL" ] && echo yes || echo no)"
[ -n "$TPL" ] || { note "register response: $REG"; note "EXPERIMENT FAIL"; exit 1; }
check "template deployed" 200 "$(code POST "/v1/templates/$TPL/deploy" '{}')"
IID=$(body POST /v1/instances "{\"template\":\"$TPL\",\"instance_key\":\"lockmetric-1\",\"params\":{},\"target_agent\":\"lockmetric-agent\"}" | jq -r '.instance_id // empty')
check "instance created" yes "$([ -n "$IID" ] && echo yes || echo no)"
[ -n "$IID" ] || { note "EXPERIMENT FAIL"; exit 1; }
code POST "/v1/instances/$IID/messages" '{"type":""}' >/dev/null

settled() { body GET "/v1/observability/nodes/$IID/$1" | jq -r '.run_summary|if .!=null and .fresh_count>0 and .active_count==0 and .pending_count==0 then "yes" else "no" end'; }
note "waiting for all four nodes to settle (blocks until they do)"
for n in slow_a slow_b slow_c claimer; do until [ "$(settled $n)" = yes ]; do sleep 0.5; done; done

note
note "== read the metrics =="
curl -sS -m 20 "$METRICS" | grep '^rimsky_claim_acquisitions_total' | sed 's/^/    /'
LOCK_ACQ=$(series 'acquirer="gate",acquirer_kind="named_lock",intent="acquired"')
LOCK_UNAVAIL=$(series 'acquirer="gate",acquirer_kind="named_lock",intent="unavailable"')
PROD_ACQ=$(series 'acquirer="files",acquirer_kind="producer",intent="acquired"')
check "named-lock acquisitions are counted, one per holder" 3 "${LOCK_ACQ:-absent}"
check "producer-claim acquisitions are counted in the same metric" 1 "${PROD_ACQ:-absent}"
check "both acquirer kinds are labels on one metric family" "named_lock,producer" \
  "$(curl -sS -m 20 "$METRICS" | grep '^rimsky_claim_acquisitions_total' | sed 's/.*acquirer_kind="\([a-z_]*\)".*/\1/' | sort -u | paste -sd, -)"
check "the lock is named on its own series" gate \
  "$(curl -sS -m 20 "$METRICS" | grep '^rimsky_claim_acquisitions_total{.*named_lock' | sed 's/.*acquirer="\([a-z_]*\)".*/\1/' | sort -u | paste -sd, -)"
check "contention is counted, so saturation is graphable" yes \
  "$([ "${LOCK_UNAVAIL:-0}" -ge 1 ] && echo yes || echo no)"
check "the counter is a prometheus counter with help text" yes \
  "$(curl -sS -m 20 "$METRICS" | grep -q '^# HELP rimsky_claim_acquisitions_total' && curl -sS -m 20 "$METRICS" | grep -q '^# TYPE rimsky_claim_acquisitions_total counter' && echo yes || echo no)"

note
if [ "$fail" = 0 ]; then note "EXPERIMENT PASS"; else note "EXPERIMENT FAIL"; fi
exit "$fail"
