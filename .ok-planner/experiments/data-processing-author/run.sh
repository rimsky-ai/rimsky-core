#!/usr/bin/env bash
# Experiment: data-processing-author
#
# A third-party claim producer carrying the typed-data mix-in (peer/, its own
# Go module whose only rimsky requirement is the protocols module, built the
# same way as the permissive-peer-build experiment's peer) advertises
# data_processing alongside claim_producer, and the run checks both halves of
# the story:
#
#   the protocol as written  -> `rimsky conformance data-processing` and
#                               `rimsky conformance claim-producer` drive every
#                               verb against the peer and pass
#   the protocol as driven   -> a fan-out node makes rimsky split the claim,
#                               stage one candidate per partition, commit them
#                               all on success, and garbage-collect them all on
#                               failure; the version history then reads back
#                               through the peer's own listing surface
set -u
cd "$(dirname "$0")"
ROOT=$(cd ../../.. && pwd)
: "${RIMSKY_IMAGE_TAG:?export RIMSKY_IMAGE_TAG=src-<tree hash> first}"
NET=exp-dataproc-net
STACK=exp-dataproc-stack
PEER=exp-dataproc-peer
KITPEER=exp-dataproc-kit-peer
PORT=${PORT:-19319}
PEER_GRPC=${PEER_GRPC:-19329}
PEER_HTTP=${PEER_HTTP:-19339}
WORK=$(mktemp -d)
BASE="http://127.0.0.1:$PORT"
PEERURL="http://127.0.0.1:$PEER_HTTP"

fail=0
note() { printf '%s\n' "$*"; }
check() {
  if [ "$2" = "$3" ]; then printf 'PASS  %-64s %s\n' "$1" "$3"
  else printf 'FAIL  %-64s expected [%s] got [%s]\n' "$1" "$2" "$3"; fail=1; fi
}
cleanup() {
  docker rm -f "$STACK" "$PEER" "$KITPEER" >/dev/null 2>&1
  docker network rm "$NET" >/dev/null 2>&1
  rm -rf "$WORK"
}
trap cleanup EXIT

req() { local m=$1 p=$2 b=${3:-}
  curl -sS -m 30 -w '\n%{http_code}' -X "$m" -H 'content-type: application/json' \
    -H "Idempotency-Key: dp-$RANDOM$RANDOM" ${b:+-d "$b"} "$BASE$p"; }
code() { req "$@" | tail -1; }
body() { req "$@" | sed '$d'; }
state() { curl -sS -m 10 "$PEERURL/state"; }
verb() { state | jq -r --arg v "$1" '.counts[$v] // 0'; }

note "== build the producer and the CLI, and bring it up =="
cp -r peer "$WORK/peer"
sed "s|RIMSKY_PROTOCOLS_PATH|$ROOT/lib/protocols|" "$WORK/peer/go.mod.tmpl" > "$WORK/peer/go.mod"
rm "$WORK/peer/go.mod.tmpl"
ARCH=$(docker info --format '{{.Architecture}}' | sed 's/aarch64/arm64/;s/x86_64/amd64/')
( cd "$WORK/peer" && GOWORK=off GOOS=linux GOARCH="$ARCH" CGO_ENABLED=0 GOFLAGS=-mod=mod GOPROXY=off GOSUMDB=off go build -o "$WORK/peer-linux" . ) \
  && check "the producer builds against the protocols module alone" yes yes \
  || { check "the producer builds against the protocols module alone" yes no; note "EXPERIMENT FAIL"; exit 1; }
( cd "$ROOT" && go build -o "$WORK/rimsky" ./cmd/rimsky/ ) || { note "CLI build failed"; exit 1; }

docker network create "$NET" >/dev/null 2>&1
docker rm -f "$STACK" "$PEER" "$KITPEER" >/dev/null 2>&1
docker run -d --name "$KITPEER" --network "$NET" -p "$PEER_GRPC:9700" \
  -e PEER_PORT=9700 -e PEER_HTTP_PORT=9701 -e PEER_LABEL=typed-kit \
  -v "$WORK/peer-linux:/peer:ro" alpine:latest /peer >/dev/null || exit 1
until nc -z 127.0.0.1 "$PEER_GRPC" 2>/dev/null; do sleep 0.5; done

note
note "== the protocol as written: the shipped conformance kits, against their own peer =="
"$WORK/rimsky" conformance data-processing --endpoint "grpc://127.0.0.1:$PEER_GRPC" > "$WORK/dp.txt" 2>&1
check "rimsky conformance data-processing passes against the peer" 0 "$?"
sed 's/^/    /' "$WORK/dp.txt"
"$WORK/rimsky" conformance claim-producer --endpoint "grpc://127.0.0.1:$PEER_GRPC" > "$WORK/cp.txt" 2>&1
check "rimsky conformance claim-producer passes against the peer" 0 "$?"
tail -3 "$WORK/cp.txt" | sed 's/^/    /'

note
note "== the protocol as driven: a fresh peer, so only rimsky's calls are counted =="
docker run -d --name "$PEER" --network "$NET" --network-alias producer -p "$PEER_HTTP:9701" \
  -e PEER_PORT=9700 -e PEER_HTTP_PORT=9701 -e PEER_LABEL=typed \
  -v "$WORK/peer-linux:/peer:ro" alpine:latest /peer >/dev/null || exit 1
until curl -sf -m 5 "$PEERURL/state" >/dev/null; do sleep 0.5; done
cat > "$WORK/rimsky.yml" <<'YAML'
persistence:
  driver: sqlite
  sqlite:
    path: /var/lib/rimsky/state.db
claim_producers:
  typed:
    endpoint: "producer:9700"
    protocols: ["claim_producer", "data_processing"]
    write_semantics_allowed: ["staged_async"]
named_locks: {}
executors:
  "typed-exec":
    transport: grpc
    endpoint: "producer:9700"
    protocols: ["executor"]
YAML
docker run -d --name "$STACK" --network "$NET" -p "$PORT:8080" \
  -v "$WORK/rimsky.yml:/etc/rimsky/rimsky.yml:ro" "rimsky-all-in-one:$RIMSKY_IMAGE_TAG" >/dev/null || exit 1
until [ "$(code GET /v1/health)" = 200 ]; do sleep 0.5; done
until [ "$(body GET /v1/observability/claim-producers | jq -r '.claim_producers[0].reachability_status')" = reachable ]; do sleep 0.5; done
check "rimsky sees the producer as a live peer" reachable \
  "$(body GET /v1/observability/claim-producers/typed | jq -r '.peer.reachability_status')"

fanout_node() { # fanout_node <type> <selector> <partition_request> <outcome>
  printf '{"type":"%s","executor":"typed-exec","claim_producers":[{"name":"typed","selector":"%s","intent":"rw","alias":"root","lifetime":"durable"}],"error_types":{"acquire/unavailable":{"action":"retry"}},"fan_out":{"claim":"root","partition_request":"%s","error_policy":{"kind":"best_effort"}},"attributes":{"schema":{"type":"object","properties":{"outcome":{"type":"string","default":"%s"},"echo":{"type":"string","default":"%s"}}}}}' "$1" "$2" "$3" "$4" "$1"
}
SPEC=$(printf '{"tag":"dataproc","spec":{"name":"dataproc","version":"1","nodes":[%s,%s]}}' \
  "$(fanout_node good dataset-good '{\"parts\":3}' ok)" \
  "$(fanout_node bad dataset-bad '{\"parts\":2}' fail)")
REG=$(body POST /v1/templates "$SPEC")
TPL=$(printf '%s' "$REG" | jq -r '.template_id // empty')
check "the fan-out template registered" yes "$([ -n "$TPL" ] && echo yes || echo no)"
[ -n "$TPL" ] || { note "register response: $REG"; note "EXPERIMENT FAIL"; exit 1; }
check "template deployed" 200 "$(code POST "/v1/templates/$TPL/deploy" '{}')"
IID=$(body POST /v1/instances "{\"template\":\"$TPL\",\"instance_key\":\"dataproc-1\",\"params\":{},\"target_agent\":\"dataproc-agent\"}" | jq -r '.instance_id // empty')
check "instance created" yes "$([ -n "$IID" ] && echo yes || echo no)"
[ -n "$IID" ] || { note "EXPERIMENT FAIL"; exit 1; }
code POST "/v1/instances/$IID/messages" '{"type":""}' >/dev/null

note "waiting for rimsky to stage a candidate per partition (blocks until it does)"
until [ "$(verb DataProcessing.BeginCandidate)" -ge 5 ]; do sleep 0.5; done
check "rimsky split the claim once per fan-out node" 2 "$(verb ClaimProducer.SplitScope)"
check "rimsky staged one candidate per partition, across both fan-outs" 5 "$(verb DataProcessing.BeginCandidate)"
note "partitions the producer handed back: $(state | jq -c '.splits')"

note "waiting for every staged candidate to be finalized or collected (blocks until it is)"
until [ "$(state | jq '.open_candidates')" -eq 0 ]; do sleep 0.5; done
STATE=$(state)
note "$(printf '%s' "$STATE" | jq -c '.counts')"
check "the successful fan-out's partitions were committed" 3 "$(printf '%s' "$STATE" | jq -r '.counts["DataProcessing.CommitCandidate"] // 0')"
check "the failing fan-out's partitions were garbage-collected" 2 "$(printf '%s' "$STATE" | jq -r '.counts["DataProcessing.AbandonCandidate"] // 0')"
check "no candidate was left staged" 0 "$(printf '%s' "$STATE" | jq '.open_candidates')"
check "a version exists for each committed partition and no other" 3 "$(printf '%s' "$STATE" | jq '.versions|length')"
check "the versions are keyed by the partitions the producer handed back" "part-0,part-1,part-2" \
  "$(printf '%s' "$STATE" | jq -r '[.versions[].partition_key]|sort|join(",")')"

note
note "== the version history reads back through the producer's listing surface =="
ASSETS=$(body GET "/v1/instances/$IID/assets")
note "assets: $(printf '%s' "$ASSETS" | jq -c '[.assets[]?|{alias,claim_handle_id}]')"
ALIAS=$(printf '%s' "$ASSETS" | jq -r '[.assets[]?|select(.alias|startswith("good"))|.alias]|first // empty')
check "the fan-out's claim is exposed as an asset" yes "$([ -n "$ALIAS" ] && echo yes || echo no)"
LISTV_BEFORE=$(verb DataProcessing.ListVersions)
VERSIONS=$(body GET "/v1/instances/$IID/assets/$ALIAS/versions")
note "versions: $(printf '%s' "$VERSIONS" | jq -c '.')"
check "reading the asset's versions called the producer's own listing verb" yes \
  "$([ "$(verb DataProcessing.ListVersions)" -gt "$LISTV_BEFORE" ] && echo yes || echo no)"
check "the CLI reads the same history" yes \
  "$("$WORK/rimsky" asset versions --endpoint "$BASE" --instance "$IID" "$ALIAS" -o json >/dev/null 2>&1 && echo yes || echo no)"

note
note "producer log:"
docker logs "$PEER" 2>&1 | tail -12 | sed 's/^/    /'

note
if [ "$fail" = 0 ]; then note "EXPERIMENT PASS"; else note "EXPERIMENT FAIL"; fi
exit "$fail"
