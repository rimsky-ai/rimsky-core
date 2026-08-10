#!/usr/bin/env bash
# Experiment: host-agent-per-run-scope-isolation
#
# A fan-out node dispatches the same late-bound executor from three sibling
# run-scopes inside one frame, concurrently. The run checks that:
#   - the agent spawns one child per run-scope, not one child shared between them
#   - each child is a distinct operating-system process, so none of them can see
#     another's in-process state
#   - each child serves exactly the calls of its own run-scope
#   - every child is reaped when its run-scope closes, with the agent still
#     running and connected
#
# The late-bound binary reports its own pid, the run-scope it was called for,
# and a counter it keeps in its own memory across calls, so a shared child would
# show up as one pid answering for two run-scopes with a counter above one.
# The fan-out claim is the bundled filesystem claim producer, which advertises
# the split-scope support a fan-out needs.

set -u
cd "$(dirname "$0")"
ROOT=$(cd ../../.. && pwd)
: "${RIMSKY_IMAGE_TAG:?export RIMSKY_IMAGE_TAG=src-<tree hash> first}"
CLI="$ROOT/bin/rimsky"
PEERSRC="$ROOT/.ok-planner/experiments/host-agent-late-bind-all-protocols/peer"
NET=exp-hars-net
STACK=exp-hars-stack
PROXY=exp-hars-proxy
PORT=${PORT:-18944}
PROXYPORT=${PROXYPORT:-18945}
WORK=$(mktemp -d)
BASE="http://127.0.0.1:$PORT"
STATE="$WORK/agent"
AGENT_LABEL=steady-badger
PARTITIONS=3

fail=0
note() { printf '%s\n' "$*"; }
check() {
  if [ "$2" = "$3" ]; then printf 'PASS  %-62s %s\n' "$1" "$3"
  else printf 'FAIL  %-62s expected [%s] got [%s]\n' "$1" "$2" "$3"; fail=1; fi
}
cleanup() {
  "$CLI" agent stop --state-dir "$STATE" >/dev/null 2>&1
  docker rm -f "$STACK" "$PROXY" >/dev/null 2>&1
  docker network rm "$NET" >/dev/null 2>&1
  rm -rf "$WORK"
}
trap cleanup EXIT

req() { local m=$1 p=$2 b=${3:-}
  curl -sS -m 20 -w '\n%{http_code}' -X "$m" -H 'content-type: application/json' \
    -H "Idempotency-Key: hars-$RANDOM" ${b:+-d "$b"} "$BASE$p"; }
code() { req "$@" | tail -1; }
body() { req "$@" | sed '$d'; }
exec_lines() { grep -E 'execute node=partitioned run_scope=' "$STATE/agent.log" 2>/dev/null || true; }
child_lines() { "$CLI" agent status --state-dir "$STATE" | sed -n 's/^ *run-scope=.*/&/p'; }

note "== build the binary every run-scope will run =="
cp -r "$PEERSRC" "$WORK/peer"
sed "s|RIMSKY_PROTOCOLS_PATH|$ROOT/lib/protocols|" "$WORK/peer/go.mod.tmpl" > "$WORK/peer/go.mod"
rm "$WORK/peer/go.mod.tmpl"
( cd "$WORK/peer" && GOFLAGS=-mod=mod GOPROXY=off GOSUMDB=off go build -o "$WORK/peer-host" . ) || exit 1
check "the binary built" yes yes

note
note "== a deployment with a fan-out-capable claim producer and a proxy =="
mkdir -p "$WORK/workspace/data"
cat > "$WORK/rimsky.yml" <<'YAML'
persistence:
  driver: sqlite
  sqlite:
    path: /var/lib/rimsky/state.db
claim_producers: {}
named_locks: {}
late_bind_service_proxies:
  executor: agent-proxy
executors:
  "agent-proxy":
    transport: grpc
    endpoint: "proxy:9090"
    protocols: ["executor", "lifecycle_subscriber"]
YAML
docker network create "$NET" >/dev/null 2>&1
docker rm -f "$STACK" "$PROXY" >/dev/null 2>&1
docker run -d --name "$PROXY" --network "$NET" --network-alias proxy -p "$PROXYPORT:9090" \
  -e RIMSKY_PROXY_GRPC_PORT=9090 \
  -e RIMSKY_CONTROL_API_URL="http://rimsky-stack:8080" \
  "rimsky-host-agent-proxy:$RIMSKY_IMAGE_TAG" >/dev/null || exit 1
until nc -z 127.0.0.1 "$PROXYPORT" 2>/dev/null; do sleep 0.5; done
docker run -d --name "$STACK" --network "$NET" --network-alias rimsky-stack -p "$PORT:8080" \
  -e RIMSKY_CLAIM_PRODUCER_FILESYSTEM_CONFIG=/etc/rimsky/claim-producer-filesystem.yml \
  -v "$PWD/claim-producer-filesystem.yml:/etc/rimsky/claim-producer-filesystem.yml:ro" \
  -v "$WORK/workspace:/workspace:rw" \
  -v "$WORK/rimsky.yml:/etc/rimsky/rimsky.yml:ro" "rimsky-all-in-one:$RIMSKY_IMAGE_TAG" >/dev/null || exit 1
until [ "$(code GET /v1/health)" = 200 ] || ! docker ps --format '{{.Names}}' | grep -q "^$STACK\$"; do sleep 0.5; done
check "the deployment is up" 200 "$(code GET /v1/health)"

mkdir -p "$STATE"
RIMSKY_ALLOW_PLAINTEXT_ENROLLMENT=1 HOME="$WORK" "$CLI" agent start \
  --proxy "127.0.0.1:$PROXYPORT" --label "$AGENT_LABEL" \
  --state-dir "$STATE" --identity-file "$STATE/identity.json" --listen 127.0.0.1:0 >"$WORK/start.out" 2>&1
check "the agent connected" 0 "$?"

note
note "== three sibling run-scopes dispatching the same late-bound service =="
SPEC=$(cat <<'JSON'
{"tag":"hars","spec":{"name":"hars","version":"1","late_bind_services":["devsvc"],"nodes":[
 {"type":"partitioned","executor":"devsvc",
  "claim_producers":[{"name":"claim-producer-filesystem","selector":"data","intent":"rw","alias":"parent"}],
  "error_types":{"acquire/unavailable":{"action":"give_up"}},
  "fan_out":{"claim":"parent",
             "partition_request":"{\"list\":[{\"key\":\"p1\"},{\"key\":\"p2\"},{\"key\":\"p3\"}]}",
             "parallelism":3,
             "error_policy":{"kind":"strict"}}}
]}}
JSON
)
TPL=$(body POST /v1/templates "$SPEC" | jq -r '.template_id // empty')
check "the fan-out template registered" yes "$([ -n "$TPL" ] && echo yes || echo no)"
check "the fan-out template deployed" 200 "$(code POST "/v1/templates/$TPL/deploy" '{}')"

# Each work unit holds its execution open, so all three run-scopes are in
# flight at once and a shared child would have to serve two of them.
CREATE=$(printf '{"template":"%s","instance_key":"ck-hars","params":{},"target_agent":"%s","service_bindings":{"devsvc":{"path":"%s","env":{"PEER_LABEL":"partition-worker","PEER_EXECUTE_DELAY_SECONDS":"8"}}}}' \
  "$TPL" "$AGENT_LABEL" "$WORK/peer-host")
IID=$(body POST /v1/instances "$CREATE" | jq -r '.instance_id // empty')
check "the instance created" yes "$([ -n "$IID" ] && echo yes || echo no)"
[ -n "$IID" ] || { note "EXPERIMENT FAIL"; exit 1; }
code POST "/v1/instances/$IID/messages" '{"type":""}' >/dev/null

note "waiting for all three run-scopes to be in flight at once (blocks until they are)"
until [ "$(exec_lines | wc -l | tr -d ' ')" -ge "$PARTITIONS" ]; do sleep 0.5; done
note "agent status while the siblings are in flight:"
"$CLI" agent status --state-dir "$STATE" | sed 's/^/    /'
LIVE_CHILDREN=$(child_lines | wc -l | tr -d ' ')
LIVE_SCOPES=$(child_lines | grep -oE 'run-scope=[0-9a-f-]+' | sort -u | wc -l | tr -d ' ')
check "the agent holds one child per sibling run-scope" "$PARTITIONS" "$LIVE_CHILDREN"
check "and every child belongs to a different run-scope" "$PARTITIONS" "$LIVE_SCOPES"
check "the children are distinct operating-system processes" "$PARTITIONS" \
  "$(pgrep -f "$WORK/peer-host" | sort -u | wc -l | tr -d ' ')"

note
note "== what each run-scope's child reported =="
exec_lines | sed 's/^.*execute/    execute/'
check "each dispatch names a different run-scope" "$PARTITIONS" \
  "$(exec_lines | grep -oE 'run_scope=[0-9a-f-]+' | sort -u | wc -l | tr -d ' ')"
check "each dispatch was served by a different process" "$PARTITIONS" \
  "$(exec_lines | grep -oE 'pid=[0-9]+' | sort -u | wc -l | tr -d ' ')"
check "no process served more than one run-scope" 1 \
  "$(exec_lines | grep -oE 'calls=[0-9]+' | sort -u | tr '\n' ' ' | grep -c '^calls=1 $')"
check "the pairing of process to run-scope is one to one" "$PARTITIONS" \
  "$(exec_lines | grep -oE '(run_scope=[0-9a-f-]+ pid=[0-9]+)' | sort -u | wc -l | tr -d ' ')"

note
note "== the children are reaped when their run-scopes close =="
settle_parent() { local s
  while :; do
    s=$(body GET "/v1/observability/nodes/$IID/partitioned" | jq -r '.run_summary as $r |
      if $r == null then "in-flight" elif $r.failed_count > 0 then "failed"
      elif $r.active_count == 0 and $r.pending_count == 0 and $r.fresh_count > 0 then "fresh"
      else "in-flight" end')
    [ "$s" = in-flight ] || { echo "$s"; return; }
    sleep 0.5
  done
}
note "waiting for the fan-out to settle (blocks until it does)"
check "the fan-out settled" fresh "$(settle_parent)"
note "waiting for the agent to reap the closed run-scopes (blocks until it has)"
until [ "$(child_lines | wc -l | tr -d ' ')" = 0 ]; do sleep 0.5; done
check "the agent holds no children once the run-scopes closed" 0 "$(child_lines | wc -l | tr -d ' ')"
check "and no spawned process is left behind" 0 \
  "$(pgrep -f "$WORK/peer-host" | wc -l | tr -d ' ')"
check "the agent itself is still connected" yes \
  "$("$CLI" agent status --state-dir "$STATE" | grep -q '^rimsky agent: connected' && echo yes || echo no)"
check "the agent spawned one child per run-scope in total" "$PARTITIONS" \
  "$(grep -c 'spawned child' "$STATE/agent.log")"

note
if [ "$fail" = 0 ]; then note "EXPERIMENT PASS"; else note "EXPERIMENT FAIL"; fi
exit "$fail"
