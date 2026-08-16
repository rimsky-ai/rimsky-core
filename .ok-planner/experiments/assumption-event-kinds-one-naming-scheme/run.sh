#!/usr/bin/env bash
# Experiment: assumption-event-kinds-one-naming-scheme
#
# An operator filtering the event feed wants prefix filtering to work: every
# claim-ish kind under one lead token, every auth kind under `auth.`. This run
# asks `GET /v1/events` for prefixes and for exact kinds, reads the vocabulary
# the API itself prints when it rejects one, and then drives a fan-out whose
# timeline carries two spellings of the same family.
set -uo pipefail

: "${RIMSKY_IMAGE_TAG:?export RIMSKY_IMAGE_TAG=src-<tree hash> first}"
TAG="$RIMSKY_IMAGE_TAG"
WORK="$(mktemp -d)"

NET=exp-assumption-evnames-net
STACK=exp-assumption-evnames-stack
PRODUCER=exp-assumption-evnames-producer

fails=0
ok()  { printf 'PASS  %s\n' "$*"; }
bad() { printf 'FAIL  %s\n' "$*"; fails=$((fails+1)); }
check() { if [ "$2" = "$3" ]; then ok "$1 [$3]"; else bad "$1 expected [$2] got [$3]"; fi; }
has() { case "$2" in *"$1"*) ok "$3";; *) bad "$3 (missing '$1')";; esac; }
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

echo "--- the kind filter is not a prefix filter"
for p in claim subclaim auth. 'claim_' 'claim*'; do
  check "kind=$p is rejected" 400 "$(code "/v1/events?kind=$p")"
done
check "kind=claim/ is accepted as a signal type-path instead" 200 "$(code '/v1/events?kind=claim/')"
check "and matches nothing" 0 "$(get '/v1/events?kind=claim/&limit=200' | jq '[.events[]]|length')"
VOCAB=$(get "/v1/events?kind=claim" | jq -r '.error')
printf '    %s\n' "$VOCAB"
has "expected an operational kind from the OperationalKind proto enum" "$VOCAB" "the rejection prints the whole accepted vocabulary"

echo "--- the claim-ish kinds are real, and sit under four different lead tokens"
for k in claim_acquired claim_held claim_resolved claim_resolution.abandon claim_resolution.commit \
         subclaim.acquired subclaim.begin_candidate orphaned_claim_lost_race orphaned_claim_released; do
  check "kind=$k is accepted" 200 "$(code "/v1/events?kind=$k")"
done
LEADS=$(printf '%s\n' claim_acquired claim_held claim_resolved claim_resolution.abandon claim_resolution.commit \
  subclaim.acquired subclaim.begin_candidate orphaned_claim_lost_race orphaned_claim_released |
  sed 's/[._].*//' | sort -u | tr '\n' ' ')
printf '    lead tokens: %s\n' "$LEADS"
check "nine claim-ish kinds share three different first words" "claim orphaned subclaim " "$LEADS"

echo "--- one vocabulary, three separator conventions"
has "claim_acquired" "$VOCAB" "snake_case kinds are in the vocabulary"
has "subclaim.acquired" "$VOCAB" "dot-separated kinds are in the same vocabulary"
has "terminal/*, transient/*, attribute/*/changed" "$VOCAB" "slash-path kinds are accepted too"

echo "--- and one family is spelled both ways"
check "fan_out_dispatched is a kind" 200 "$(code /v1/events?kind=fan_out_dispatched)"
check "fanout.children_created is a kind" 200 "$(code /v1/events?kind=fanout.children_created)"
check "fanout_children_created is not" 400 "$(code /v1/events?kind=fanout_children_created)"
check "fan_out.dispatched is not" 400 "$(code /v1/events?kind=fan_out.dispatched)"

SPEC=$(cat <<'JSON'
{"tag":"ev","spec":{"name":"ev","version":"1","params_schema":{"type":"object","properties":{"items":{"type":"array"}}},
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
IID=$(post /v1/instances "$(printf '{"template":"%s","instance_key":"k-ev","target_agent":"audit-agent","params":{"items":[{"key":"a"},{"key":"b"}]}}' "$TPL")" | jq -r '.instance_id // empty')
post "/v1/instances/$IID/messages" '{}' >/dev/null
while :; do
  KINDS=$(get "/v1/events?instance_id=$IID&limit=300" | jq -r '[.events[].kind]|unique|join(" ")')
  case "$KINDS" in *subclaim.acquired*) break;; esac
  sleep 0.5
done
printf '    kinds in one fan-out timeline: %s\n' "$KINDS"

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

has "fan_out_dispatched" "$KINDS" "the timeline carries the snake_case spelling"
has "fanout.children_created" "$KINDS" "and the dotted spelling of the same family"
has "subclaim.acquired" "$KINDS" "and a dotted claim-family kind"
has "claim_resolution.commit" "$LKINDS" "and a mixed snake-plus-dotted claim kind"
has "lock_acquired" "$LKINDS" "beside a plain snake_case one"

echo
if [ "$fails" -eq 0 ]; then echo "EXPERIMENT PASS"; else echo "EXPERIMENT FAIL ($fails)"; fi
exit "$fails"
