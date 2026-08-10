#!/usr/bin/env bash
# Experiment: anonymous-agents-isolated
#
# Two developers share one anonymous-mode deployment. Each runs their own
# host-agent against the same host-agent-proxy and creates their own instance
# targeting their own agent. The run checks that:
#   - both agents connect at once and stay connected (no displacement)
#   - each instance's dispatch is served by the binary that agent spawned, and
#     only by that one (no cross-talk)
#   - each agent's spawned children are its own
#   - an instance targeting an agent nobody is running does not get served by
#     somebody else's agent
#
# The late-bound service is the third-party peer built for permissive-peer-build;
# each agent is started with a different PEER_LABEL in its environment, so the
# attribute the dispatch writes back names which developer's agent served it.

set -u
cd "$(dirname "$0")"
ROOT=$(cd ../../.. && pwd)
: "${RIMSKY_IMAGE_TAG:?export RIMSKY_IMAGE_TAG=src-<tree hash> first}"
CLI="$ROOT/bin/rimsky"
PEERSRC="$ROOT/.ok-planner/experiments/permissive-peer-build/peer"
NET=exp-anon-net
STACK=exp-anon-stack
PROXY=exp-anon-proxy
PORT=${PORT:-18551}
PROXYPORT=${PROXYPORT:-18552}
WORK=$(mktemp -d)
BASE="http://127.0.0.1:$PORT"
ALPHA=sparkling-wombat
BETA=grumpy-otter

fail=0
note() { printf '%s\n' "$*"; }
check() {
  if [ "$2" = "$3" ]; then printf 'PASS  %-62s %s\n' "$1" "$3"
  else printf 'FAIL  %-62s expected [%s] got [%s]\n' "$1" "$2" "$3"; fail=1; fi
}
cleanup() {
  "$CLI" agent stop --state-dir "$WORK/agent-alpha" >/dev/null 2>&1
  "$CLI" agent stop --state-dir "$WORK/agent-beta" >/dev/null 2>&1
  docker rm -f "$STACK" "$PROXY" >/dev/null 2>&1
  docker network rm "$NET" >/dev/null 2>&1
  rm -rf "$WORK"
}
trap cleanup EXIT

req() { local m=$1 p=$2 b=${3:-}
  curl -sS -m 15 -w '\n%{http_code}' -X "$m" -H 'content-type: application/json' \
    -H "Idempotency-Key: anon-$RANDOM" ${b:+-d "$b"} "$BASE$p"; }
code() { req "$@" | tail -1; }
body() { req "$@" | sed '$d'; }
settle() { # settle <instance id> -> fresh|failed  (blocks until the node settles)
  local iid=$1 s
  while :; do
    s=$(body GET "/v1/observability/nodes/$iid/worker" | jq -r '.run_summary as $r | if $r == null then "in-flight" elif $r.failed_count > 0 then "failed" elif $r.fresh_count > 0 and $r.active_count == 0 and $r.pending_count == 0 then "fresh" else "in-flight" end')
    [ "$s" = in-flight ] || { echo "$s"; return; }
    sleep 0.5
  done
}
served_by() { body GET "/v1/observability/nodes/$1/worker" | jq -r '.latest_attributes.served_by // empty'; }

note "== build the late-bound service each developer runs =="
cp -r "$PEERSRC" "$WORK/peer"
sed "s|RIMSKY_PROTOCOLS_PATH|$ROOT/lib/protocols|" "$WORK/peer/go.mod.tmpl" > "$WORK/peer/go.mod"
rm "$WORK/peer/go.mod.tmpl"
( cd "$WORK/peer" && GOFLAGS=-mod=mod GOPROXY=off GOSUMDB=off go build -o "$WORK/peer-host" . ) || exit 1
check "late-bound service binary built" yes yes

note
note "== an anonymous-mode deployment with a host-agent-proxy =="
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
check "the deployment is in anonymous mode" anonymous "$(body GET /v1/auth/status | jq -r .mode)"
docker run -d --name "$PROXY" --network "$NET" --network-alias proxy -p "$PROXYPORT:9090" \
  -e RIMSKY_PROXY_GRPC_PORT=9090 \
  -e RIMSKY_CONTROL_API_URL="http://rimsky-stack:8080" \
  "rimsky-host-agent-proxy:$RIMSKY_IMAGE_TAG" >/dev/null || exit 1
until nc -z 127.0.0.1 "$PROXYPORT" 2>/dev/null; do sleep 0.5; done
check "the proxy is listening for agents" yes yes

note
note "== two developers, two agents, one proxy =="
# The agent hands each child it spawns a loopback enrolment endpoint of its own,
# so a child using the shipped peer-auth helper needs the plaintext-enrolment
# opt-in for that hop; it is inherited from the agent's environment.
start_agent() { # start_agent <state dir> <label> <peer label>
  PEER_LABEL="$3" RIMSKY_ALLOW_PLAINTEXT_ENROLLMENT=1 HOME="$WORK" "$CLI" agent start \
    --proxy "127.0.0.1:$PROXYPORT" --label "$2" \
    --state-dir "$1" --identity-file "$1/identity.json" --listen 127.0.0.1:0
}
start_agent "$WORK/agent-alpha" "$ALPHA" alpha-peer > "$WORK/alpha.start" 2>&1
check "developer A's agent connected" 0 "$?"
sed 's/^/    A: /' "$WORK/alpha.start"
start_agent "$WORK/agent-beta" "$BETA" beta-peer > "$WORK/beta.start" 2>&1
check "developer B's agent connected" 0 "$?"
sed 's/^/    B: /' "$WORK/beta.start"
check "A's agent is still connected after B joined" connected \
  "$("$CLI" agent status --state-dir "$WORK/agent-alpha" | sed -n 's/rimsky agent: \([a-z]*\).*/\1/p')"
check "B's agent is connected" connected \
  "$("$CLI" agent status --state-dir "$WORK/agent-beta" | sed -n 's/rimsky agent: \([a-z]*\).*/\1/p')"

note
note "== each developer's instance targets their own agent =="
SPEC='{"tag":"anoniso","spec":{"name":"anoniso","version":"1","late_bind_services":["devsvc"],"nodes":[{"type":"worker","executor":"devsvc"}]}}'
TPL=$(body POST /v1/templates "$SPEC" | jq -r '.template_id // empty')
check "late-binding template registered" yes "$([ -n "$TPL" ] && echo yes || echo no)"
check "template deployed" 200 "$(code POST "/v1/templates/$TPL/deploy" '{}')"

mk() { # mk <instance key> <target agent> -> instance id
  printf '{"template":"%s","instance_key":"%s","params":{},"target_agent":"%s","service_bindings":{"devsvc":{"path":"%s"}}}' \
    "$TPL" "$1" "$2" "$WORK/peer-host" > "$WORK/create-$1.json"
  body POST /v1/instances "$(cat "$WORK/create-$1.json")" | jq -r '.instance_id // empty'
}
IA=$(mk ck-alpha "$ALPHA")
IB=$(mk ck-beta "$BETA")
check "developer A's instance created" yes "$([ -n "$IA" ] && echo yes || echo no)"
check "developer B's instance created" yes "$([ -n "$IB" ] && echo yes || echo no)"
check "A's instance is stamped with A's agent" "$ALPHA" "$(body GET "/v1/instances/$IA" | jq -r '.target_routing_identity // .instance.target_routing_identity // empty')"
check "B's instance is stamped with B's agent" "$BETA" "$(body GET "/v1/instances/$IB" | jq -r '.target_routing_identity // .instance.target_routing_identity // empty')"
code POST "/v1/instances/$IA/messages" '{"type":""}' >/dev/null
code POST "/v1/instances/$IB/messages" '{"type":""}' >/dev/null

note "waiting for both dispatches to settle (blocks until they do)"
SA=$(settle "$IA"); SB=$(settle "$IB")
check "A's node settled" fresh "$SA"
check "B's node settled" fresh "$SB"
check "A's dispatch was served by A's own agent" alpha-peer "$(served_by "$IA")"
check "B's dispatch was served by B's own agent" beta-peer "$(served_by "$IB")"

note
note "== each agent served exactly its own dispatch =="
ALOG="$WORK/agent-alpha/agent.log"; BLOG="$WORK/agent-beta/agent.log"
check "A's agent spawned the service once" 1 "$(grep -c 'spawned child' "$ALOG")"
check "B's agent spawned the service once" 1 "$(grep -c 'spawned child' "$BLOG")"
check "A's child announced itself as A's peer" 1 "$(grep -c 'peer "alpha-peer"' "$ALOG")"
check "B's child announced itself as B's peer" 1 "$(grep -c 'peer "beta-peer"' "$BLOG")"
check "A's agent executed exactly one dispatch" 1 "$(grep -c 'execute node=worker' "$ALOG")"
check "B's agent executed exactly one dispatch" 1 "$(grep -c 'execute node=worker' "$BLOG")"
check "A's agent is still connected" connected \
  "$("$CLI" agent status --state-dir "$WORK/agent-alpha" | sed -n 's/rimsky agent: \([a-z]*\).*/\1/p')"
check "B's agent is still connected" connected \
  "$("$CLI" agent status --state-dir "$WORK/agent-beta" | sed -n 's/rimsky agent: \([a-z]*\).*/\1/p')"

note "== an instance aimed at nobody's agent is served by nobody =="
IC=$(mk ck-ghost quiet-badger)
check "the third instance was created" yes "$([ -n "$IC" ] && echo yes || echo no)"
code POST "/v1/instances/$IC/messages" '{"type":""}' >/dev/null
note "waiting for the unrouted dispatch to settle (blocks until it does)"
SC=$(settle "$IC")
check "it did not succeed on somebody else's agent" failed "$SC"
check "and no agent's binary served it" "" "$(served_by "$IC")"
check "A's dispatch is still attributed to A" alpha-peer "$(served_by "$IA")"
check "B's dispatch is still attributed to B" beta-peer "$(served_by "$IB")"
check "and neither agent picked the unrouted dispatch up" "1 1" \
  "$(printf '%s %s' "$(grep -c 'execute node=worker' "$ALOG")" "$(grep -c 'execute node=worker' "$BLOG")")"

note
if [ "$fail" = 0 ]; then note "EXPERIMENT PASS"; else note "EXPERIMENT FAIL"; fi
exit "$fail"
