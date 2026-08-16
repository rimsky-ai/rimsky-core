#!/usr/bin/env bash
# Experiment: assumption-event-kinds-paired
#
# An operator reconciling a timeline expects every acquisition kind to have a
# release counterpart, the way `lock_acquired` has `lock_released`. This run
# drives a fan-out that takes a named lock, acquires a claim and splits it into
# a subclaim, runs to completion, and then reads the kinds the timeline
# actually carries -- and asks the API whether the missing counterparts are
# kinds at all.
set -uo pipefail

: "${RIMSKY_IMAGE_TAG:?export RIMSKY_IMAGE_TAG=src-<tree hash> first}"
TAG="$RIMSKY_IMAGE_TAG"
WORK="$(mktemp -d)"

NET=exp-assumption-evpair-net
STACK=exp-assumption-evpair-stack
PRODUCER=exp-assumption-evpair-producer

fails=0
ok()  { printf 'PASS  %s\n' "$*"; }
bad() { printf 'FAIL  %s\n' "$*"; fails=$((fails+1)); }
check() { if [ "$2" = "$3" ]; then ok "$1 [$3]"; else bad "$1 expected [$2] got [$3]"; fi; }
has() { case "$2" in *"$1"*) ok "$3";; *) bad "$3 (missing '$1')";; esac; }
hasnt() { case "$2" in *"$1"*) bad "$3 (found '$1')";; *) ok "$3";; esac; }
free_port() { python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()'; }
uid() { python3 -c 'import uuid;print(uuid.uuid4().hex)'; }

cleanup() {
  docker rm -f "$STACK" "$PRODUCER" >/dev/null 2>&1
  docker network rm "$NET" >/dev/null 2>&1
  rm -rf "$WORK"
}
trap cleanup EXIT

mkdir -p "$WORK/data/work" "$WORK/data/solo"
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
named_locks:
  "gate":
    limit: 1
executors: {}
claim_producers:
  "files":
    endpoint: "producer:9100"
    protocols: ["claim_producer"]
    write_semantics_allowed: ["sync"]
    observability_endpoint: "producer:9100"
YAML

PORT=$(free_port); BASE="http://127.0.0.1:$PORT"
docker rm -f "$STACK" "$PRODUCER" >/dev/null 2>&1
docker network rm "$NET" >/dev/null 2>&1
docker network create "$NET" >/dev/null || exit 1
docker run -d --name "$PRODUCER" --network "$NET" --network-alias producer \
  -e RIMSKY_CLAIM_PRODUCER_FILESYSTEM_CONFIG=/etc/rimsky/fs.yml \
  -v "$WORK/fs.yml:/etc/rimsky/fs.yml:ro" -v "$WORK/data:/workspace/data" \
  "rimsky-claim-producer-filesystem:$TAG" >/dev/null || exit 1
docker run -d --name "$STACK" --network "$NET" --network-alias rimsky \
  -e RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_HOST=rimsky -p "127.0.0.1:$PORT:8080" \
  -v "$WORK/rimsky.yml:/etc/rimsky/rimsky.yml:ro" "rimsky-all-in-one:$TAG" >/dev/null || exit 1

get()  { curl -s "$BASE$1"; }
code() { curl -s -o /dev/null -w '%{http_code}' "$BASE$1"; }
post() { curl -s -X POST -H 'content-type: application/json' -H "Idempotency-Key: $(uid)" -d "$2" "$BASE$1"; }
until [ "$(code /v1/health)" = 200 ]; do sleep 0.5; done

SPEC=$(cat <<'JSON'
{"tag":"pair","spec":{"name":"pair","version":"1","params_schema":{"type":"object","properties":{"items":{"type":"array"}}},
 "nodes":[
  {"type":"producer","kind":"attribute_passthrough","attributes":{"schema":{"type":"object","properties":{"items":{"type":"array","source":"{{params.items}}"}}}}},
  {"type":"fan_parent","kind":"attribute_passthrough",
   "subscribes":[{"node":"producer","type":"attribute/items/changed","force_upstream_refresh":false}],
   "claim_producers":[{"name":"files","selector":"work","intent":"rw","alias":"data"}],
   "error_types":{"acquire/unavailable":{"action":"give_up"}},
   "fan_out":{"claim":"data","partition_request":"{\"list\":{{nodes.producer.attribute.items}}}","error_policy":{"kind":"best_effort"}},
   "attributes":{"schema":{"type":"object","properties":{"pk":{"type":"string","source":"{{child.partition_key}}"}}}}}
 ]}}
JSON
)
TPL=$(post /v1/templates "$SPEC" | jq -r '.template_id // empty')
[ -n "$TPL" ] || { echo "template registration failed"; exit 1; }
post "/v1/templates/$TPL/deploy" '{}' >/dev/null
IID=$(post /v1/instances "$(printf '{"template":"%s","instance_key":"k-pair","target_agent":"audit-agent","params":{"items":[{"key":"a"},{"key":"b"}]}}' "$TPL")" | jq -r '.instance_id // empty')
post "/v1/instances/$IID/messages" '{}' >/dev/null
while :; do
  KINDS=$(get "/v1/events?instance_id=$IID&limit=300" | jq -r '[.events[].kind]|unique|join(" ")')
  case "$KINDS" in *subclaim.acquired*) break;; esac
  sleep 0.5
done
printf '    %s\n' "$KINDS"

LOCK_SPEC='{"tag":"lk","spec":{"name":"lk","version":"1","nodes":[{"type":"held","kind":"attribute_passthrough","locks":[{"name":"gate"}],"claim_producers":[{"name":"files","selector":"solo","intent":"rw","alias":"data"}],"error_types":{"acquire/unavailable":{"action":"give_up"}},"attributes":{"schema":{"type":"object","properties":{"v":{"type":"integer","default":1}}}}}]}}'
LTPL=$(post /v1/templates "$LOCK_SPEC" | jq -r '.template_id // empty')
[ -n "$LTPL" ] || { echo "lock template registration failed"; exit 1; }
post "/v1/templates/$LTPL/deploy" '{}' >/dev/null
LIID=$(post /v1/instances "$(printf '{"template":"%s","instance_key":"k-lock","target_agent":"audit-agent","params":{}}' "$LTPL")" | jq -r '.instance_id // empty')
post "/v1/instances/$LIID/messages" '{}' >/dev/null
while :; do
  LKINDS=$(get "/v1/events?instance_id=$LIID&limit=300" | jq -r '[.events[].kind]|unique|join(" ")')
  case "$LKINDS" in *claim_resolution.commit*) break;; esac
  sleep 0.5
done
printf '    claim-and-lock timeline: %s\n' "$LKINDS"


echo "--- the lock pair the prior reasons from is real"
has "lock_acquired" "$LKINDS" "the run acquired a named lock"
has "lock_released" "$LKINDS" "and released it under the matching kind"

echo "--- the claim side acquires under one name and ends under another"
has "subclaim.acquired" "$KINDS" "the fan-out acquired a subclaim"
hasnt "subclaim.released" "$KINDS" "no subclaim release kind appears"
has "claim_resolution.commit" "$LKINDS" "the claim ends as a resolution, not a release"

echo "--- and the missing counterparts are not kinds at all"
for k in claim_released subclaim.released subclaim.resolved subclaim.resolution.commit claim_resolution.release; do
  check "kind=$k is rejected as unknown" 400 "$(code "/v1/events?kind=$k")"
done
check "kind=lock_released is accepted" 200 "$(code /v1/events?kind=lock_released)"

echo "--- the one claim acquisition kind that exists stayed empty through a claim's whole life"
for k in claim_acquired claim_held; do
  check "kind=$k is a valid kind" 200 "$(code "/v1/events?kind=$k")"
  check "kind=$k returned no rows after a claim was acquired and committed" 0 \
    "$(get "/v1/events?kind=$k&limit=200" | jq '[.events[]]|length')"
done
check "claim_resolution.commit did return rows" 1 \
  "$(get "/v1/events?kind=claim_resolution.commit&limit=200" | jq 'if ([.events[]]|length) > 0 then 1 else 0 end')"

echo
if [ "$fails" -eq 0 ]; then echo "EXPERIMENT PASS"; else echo "EXPERIMENT FAIL ($fails)"; fi
exit "$fails"
