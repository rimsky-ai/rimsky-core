#!/usr/bin/env bash
# Experiment: host-agent-control-plane
#
# An operator drives the host-agent's whole lifecycle from the same CLI that
# drives the rimsky stack. The run checks that:
#   - `rimsky agent status` reports the agent is not running before it starts
#   - `rimsky agent start` connects the agent to the deployment's proxy and says so
#   - `rimsky agent status` reports the connection, the proxy it is connected to,
#     and the children the agent has spawned (none, then one)
#   - `rimsky agent stop` stops the agent and reaps the child it spawned
#   - after stopping, status reports not running and the spawned process is gone
#
# The late-bound service is the peer built for host-agent-late-bind-all-protocols,
# which writes its own pid to a file so the run can watch the child process
# itself rather than the agent's account of it.

set -u
cd "$(dirname "$0")"
ROOT=$(cd ../../.. && pwd)
: "${RIMSKY_IMAGE_TAG:?export RIMSKY_IMAGE_TAG=src-<tree hash> first}"
CLI="$ROOT/bin/rimsky"
PEERSRC="$ROOT/.ok-planner/experiments/host-agent-late-bind-all-protocols/peer"
NET=exp-hacp-net
STACK=exp-hacp-stack
PROXY=exp-hacp-proxy
PORT=${PORT:-18935}
PROXYPORT=${PROXYPORT:-18936}
WORK=$(mktemp -d)
BASE="http://127.0.0.1:$PORT"
STATE="$WORK/agent"
AGENT_LABEL=curious-marmot

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
  curl -sS -m 15 -w '\n%{http_code}' -X "$m" -H 'content-type: application/json' \
    -H "Idempotency-Key: hacp-$RANDOM" ${b:+-d "$b"} "$BASE$p"; }
code() { req "$@" | tail -1; }
body() { req "$@" | sed '$d'; }
agent_state() { "$CLI" agent status --state-dir "$STATE" | sed -n 's/^rimsky agent: \([a-z][a-z ]*[a-z]\).*/\1/p' | head -1; }
child_lines() { "$CLI" agent status --state-dir "$STATE" | sed -n 's/^ *run-scope=.*/&/p'; }

note "== build the local service the operator will late-bind =="
cp -r "$PEERSRC" "$WORK/peer"
sed "s|RIMSKY_PROTOCOLS_PATH|$ROOT/lib/protocols|" "$WORK/peer/go.mod.tmpl" > "$WORK/peer/go.mod"
rm "$WORK/peer/go.mod.tmpl"
( cd "$WORK/peer" && GOFLAGS=-mod=mod GOPROXY=off GOSUMDB=off go build -o "$WORK/peer-host" . ) || exit 1
check "the local service binary built" yes yes

note
note "== a deployment with a host-agent proxy =="
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
docker run -d --name "$STACK" --network "$NET" --network-alias rimsky-stack -p "$PORT:8080" \
  -v "$WORK/rimsky.yml:/etc/rimsky/rimsky.yml:ro" "rimsky-all-in-one:$RIMSKY_IMAGE_TAG" >/dev/null || exit 1
until [ "$(code GET /v1/health)" = 200 ]; do sleep 0.5; done
docker run -d --name "$PROXY" --network "$NET" --network-alias proxy -p "$PROXYPORT:9090" \
  -e RIMSKY_PROXY_GRPC_PORT=9090 \
  -e RIMSKY_CONTROL_API_URL="http://rimsky-stack:8080" \
  "rimsky-host-agent-proxy:$RIMSKY_IMAGE_TAG" >/dev/null || exit 1
until nc -z 127.0.0.1 "$PROXYPORT" 2>/dev/null; do sleep 0.5; done
check "the deployment is up" 200 "$(code GET /v1/health)"
check "the proxy is listening for agents" yes yes

note
note "== the operator starts, inspects and stops the agent =="
mkdir -p "$STATE"
check "before starting, status reports the agent is not running" "not running" "$(agent_state)"

# The agent hands each child it spawns a loopback enrolment endpoint of its own,
# so a child using the shipped peer-auth helper needs the plaintext-enrolment
# opt-in for that hop; it is inherited from the agent's environment.
START_OUT=$(RIMSKY_ALLOW_PLAINTEXT_ENROLLMENT=1 HOME="$WORK" "$CLI" agent start \
  --proxy "127.0.0.1:$PROXYPORT" --label "$AGENT_LABEL" \
  --state-dir "$STATE" --identity-file "$STATE/identity.json" --listen 127.0.0.1:0 2>&1)
START_RC=$?
note "    agent start said: $START_OUT"
check "starting the agent succeeds" 0 "$START_RC"
check "and it reports the proxy it connected to" yes \
  "$(printf '%s' "$START_OUT" | grep -q "connected to 127.0.0.1:$PROXYPORT" && echo yes || echo no)"
check "status now reports the agent connected" connected "$(agent_state)"
check "status names the proxy" yes \
  "$("$CLI" agent status --state-dir "$STATE" | grep -q "proxy 127.0.0.1:$PROXYPORT" && echo yes || echo no)"
check "status reports no spawned children yet" yes \
  "$("$CLI" agent status --state-dir "$STATE" | grep -q 'spawned children: none' && echo yes || echo no)"

note
note "== a dispatch makes the agent spawn a child =="
SPEC='{"tag":"hacp","spec":{"name":"hacp","version":"1","late_bind_services":["devsvc"],"nodes":[{"type":"worker","executor":"devsvc"}]}}'
TPL=$(body POST /v1/templates "$SPEC" | jq -r '.template_id // empty')
check "the late-binding template registered" yes "$([ -n "$TPL" ] && echo yes || echo no)"
check "the template deployed" 200 "$(code POST "/v1/templates/$TPL/deploy" '{}')"
CREATE=$(printf '{"template":"%s","instance_key":"ck-hacp","params":{},"target_agent":"%s","service_bindings":{"devsvc":{"path":"%s","env":{"PEER_PID_FILE":"%s","PEER_EXECUTE_DELAY_SECONDS":"25"}}}}' \
  "$TPL" "$AGENT_LABEL" "$WORK/peer-host" "$WORK/child.pid")
IID=$(body POST /v1/instances "$CREATE" | jq -r '.instance_id // empty')
check "the instance created" yes "$([ -n "$IID" ] && echo yes || echo no)"
[ -n "$IID" ] || { note "EXPERIMENT FAIL"; exit 1; }
code POST "/v1/instances/$IID/messages" '{"type":""}' >/dev/null

note "waiting for the agent to spawn the child (blocks until status lists one)"
until [ "$(child_lines | wc -l | tr -d ' ')" -ge 1 ]; do sleep 0.5; done
CHILD_PID=$(cat "$WORK/child.pid" 2>/dev/null || echo "")
check "the spawned child wrote its pid" yes "$([ -n "$CHILD_PID" ] && echo yes || echo no)"
check "the child process is alive" yes "$(kill -0 "$CHILD_PID" 2>/dev/null && echo yes || echo no)"

note "agent status while the child is live:"
"$CLI" agent status --state-dir "$STATE" | sed 's/^/    /'
check "status lists exactly one spawned child" 1 "$(child_lines | wc -l | tr -d ' ')"
check "and names the binary the operator bound" yes \
  "$(child_lines | grep -q "binding=$WORK/peer-host" && echo yes || echo no)"
check "and names the run-scope it belongs to" yes \
  "$(child_lines | grep -qE 'run-scope=[0-9a-f-]{36}' && echo yes || echo no)"

note
note "== the operator stops the agent =="
STOP_OUT=$("$CLI" agent stop --state-dir "$STATE" 2>&1)
STOP_RC=$?
note "    agent stop said: $STOP_OUT"
check "stopping the agent succeeds" 0 "$STOP_RC"
check "and it reports the agent stopped" yes \
  "$(printf '%s' "$STOP_OUT" | grep -q 'stopped' && echo yes || echo no)"
check "the child it spawned is reaped" no "$(kill -0 "$CHILD_PID" 2>/dev/null && echo yes || echo no)"
check "no process is left holding the bound binary open" 0 \
  "$(pgrep -f "$WORK/peer-host" | wc -l | tr -d ' ')"
check "status reports the agent is not running again" "not running" "$(agent_state)"
check "stopping an already-stopped agent is not an error" 0 \
  "$("$CLI" agent stop --state-dir "$STATE" >/dev/null 2>&1; echo $?)"

note
if [ "$fail" = 0 ]; then note "EXPERIMENT PASS"; else note "EXPERIMENT FAIL"; fi
exit "$fail"
