#!/usr/bin/env bash
# Experiment: assumption-event-kinds-filterable-by-instance-and-node
#
# An operator debugging one node expects to narrow the feed to that node and
# still see everything. This run drives a graph, terminates the instance, then
# reads the whole feed and counts, per kind, how many rows carry an instance id
# and how many carry a node id -- and checks what a node filter does to the
# rows that carry neither.
set -uo pipefail

: "${RIMSKY_IMAGE_TAG:?export RIMSKY_IMAGE_TAG=src-<tree hash> first}"
TAG="$RIMSKY_IMAGE_TAG"
WORK="$(mktemp -d)"

NET=exp-assumption-evids-net
STACK=exp-assumption-evids-stack

fails=0
ok()  { printf 'PASS  %s\n' "$*"; }
bad() { printf 'FAIL  %s\n' "$*"; fails=$((fails+1)); }
check() { if [ "$2" = "$3" ]; then ok "$1 [$3]"; else bad "$1 expected [$2] got [$3]"; fi; }
free_port() { python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()'; }
uid() { python3 -c 'import uuid;print(uuid.uuid4().hex)'; }

cleanup() {
  docker rm -f "$STACK" >/dev/null 2>&1
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
executors: {}
YAML

PORT=$(free_port); BASE="http://127.0.0.1:$PORT"
docker rm -f "$STACK" >/dev/null 2>&1
docker network rm "$NET" >/dev/null 2>&1
docker network create "$NET" >/dev/null || exit 1
docker run -d --name "$STACK" --network "$NET" --network-alias rimsky \
  -e RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_HOST=rimsky -p "127.0.0.1:$PORT:8080" \
  -v "$WORK/rimsky.yml:/etc/rimsky/rimsky.yml:ro" "rimsky-all-in-one:$TAG" >/dev/null || exit 1

get()  { curl -s "$BASE$1"; }
code() { curl -s -o /dev/null -w '%{http_code}' "$BASE$1"; }
post() { curl -s -X POST -H 'content-type: application/json' -H "Idempotency-Key: $(uid)" -d "$2" "$BASE$1"; }
until [ "$(code /v1/health)" = 200 ]; do sleep 0.5; done

SPEC='{"tag":"ids","spec":{"name":"ids","version":"1","nodes":[{"type":"w","kind":"attribute_passthrough","attributes":{"schema":{"type":"object","properties":{"v":{"type":"integer","default":1}}}}}]}}'
TPL=$(post /v1/templates "$SPEC" | jq -r '.template_id // empty')
[ -n "$TPL" ] || { echo "template registration failed"; exit 1; }
post "/v1/templates/$TPL/deploy" '{}' >/dev/null
IID=$(post /v1/instances "$(printf '{"template":"%s","instance_key":"k-ids","target_agent":"audit-agent","params":{}}' "$TPL")" | jq -r '.instance_id // empty')
post "/v1/instances/$IID/messages" '{}' >/dev/null
while :; do
  s=$(get "/v1/observability/nodes/$IID/w" | jq -r '.run_summary as $r | if $r == null then "in-flight" elif $r.failed_count>0 then "failed" elif $r.fresh_count>0 and $r.active_count==0 and $r.pending_count==0 then "fresh" else "in-flight" end')
  [ "$s" = in-flight ] || break
  sleep 0.4
done
post "/v1/instances/$IID/terminate" '{}' >/dev/null
while :; do
  n=$(get "/v1/events?kind=instance_terminated&limit=50" | jq '[.events[]]|length')
  [ "$n" -gt 0 ] && break
  sleep 0.4
done

echo "--- per-kind identifier coverage over the whole feed"
get "/v1/events?limit=500" | jq -r '[.events[] | {k:.kind, i:((.instance_id//"")|length>0), n:((.node_id//"")|length>0)}] | group_by(.k) | map({kind:.[0].k, rows:length, inst:(map(select(.i))|length), node:(map(select(.n))|length)}) | .[] | "    \(.kind)  rows=\(.rows) with_instance=\(.inst) with_node=\(.node)"'

AUTH_ROWS=$(get "/v1/events?kind=auth.access_attempted&limit=500" | jq '[.events[]]|length')
AUTH_WITH_INST=$(get "/v1/events?kind=auth.access_attempted&limit=500" | jq '[.events[] | select(((.instance_id//"")|length)>0)]|length')
AUTH_WITH_NODE=$(get "/v1/events?kind=auth.access_attempted&limit=500" | jq '[.events[] | select(((.node_id//"")|length)>0)]|length')
echo "--- an auth kind carries neither identifier"
check "the feed has auth.access_attempted rows" 1 "$([ "$AUTH_ROWS" -gt 0 ] && echo 1 || echo 0)"
check "none of them carries an instance id" 0 "$AUTH_WITH_INST"
check "none of them carries a node id" 0 "$AUTH_WITH_NODE"

echo "--- an instance-scoped kind can still lack a node id"
TERM_WITH_INST=$(get "/v1/events?kind=instance_terminated&limit=50" | jq '[.events[] | select(((.instance_id//"")|length)>0)]|length')
TERM_WITH_NODE=$(get "/v1/events?kind=instance_terminated&limit=50" | jq '[.events[] | select(((.node_id//"")|length)>0)]|length')
check "instance_terminated carries an instance id" 1 "$TERM_WITH_INST"
check "instance_terminated carries no node id" 0 "$TERM_WITH_NODE"

echo "--- narrowing to one node drops those rows without saying so"
ND=$(get "/v1/events?kind=work_started&limit=1" | jq -r '.events[0].node_id')
check "a node id is available to filter on" 1 "$([ -n "$ND" ] && [ "$ND" != null ] && echo 1 || echo 0)"
check "the node-filtered request succeeds" 200 "$(code "/v1/events?kind=auth.access_attempted&node_id=$ND")"
check "and returns no auth rows at all" 0 "$(get "/v1/events?kind=auth.access_attempted&node_id=$ND&limit=500" | jq '[.events[]]|length')"
check "the same filter returns the node's own rows" 1 \
  "$(get "/v1/events?kind=work_started&node_id=$ND&limit=500" | jq 'if ([.events[]]|length) > 0 then 1 else 0 end')"
check "narrowing to the instance still drops the auth rows" 0 \
  "$(get "/v1/events?kind=auth.access_attempted&instance_id=$IID&limit=500" | jq '[.events[]]|length')"

echo
if [ "$fails" -eq 0 ]; then echo "EXPERIMENT PASS"; else echo "EXPERIMENT FAIL ($fails)"; fi
exit "$fails"
