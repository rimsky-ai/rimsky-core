#!/usr/bin/env bash
# Experiment: host-agent-anonymous-mode
#
# An operator brings up a fresh rimsky deployment, connects a host-agent, and
# runs a late-bound service against it without minting a single credential. The
# run checks that:
#   - the fresh deployment is in anonymous mode with no api-keys
#   - the agent registers with the proxy carrying no api-key at all, and adopts
#     the routing label the operator asked for
#   - a template naming a late-bound service registers and deploys unauthenticated
#   - an instance created unauthenticated, targeting that agent by its label,
#     dispatches to the operator's own local binary and settles
#   - an anonymous-mode instance must name a target agent, and one aimed at an
#     agent nobody is running is not served by the connected agent
#   - no api-key exists at any point, before or after the work
#
# The late-bound binary is the local service built for
# host-agent-late-bind-all-protocols.

set -u
cd "$(dirname "$0")"
ROOT=$(cd ../../.. && pwd)
: "${RIMSKY_IMAGE_TAG:?export RIMSKY_IMAGE_TAG=src-<tree hash> first}"
CLI="$ROOT/bin/rimsky"
PEERSRC="$ROOT/.ok-planner/experiments/host-agent-late-bind-all-protocols/peer"
NET=exp-haam-net
STACK=exp-haam-stack
PROXY=exp-haam-proxy
PORT=${PORT:-18947}
PROXYPORT=${PROXYPORT:-18948}
WORK=$(mktemp -d)
BASE="http://127.0.0.1:$PORT"
STATE="$WORK/agent"
AGENT_LABEL=plucky-vole

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

# Every request in this run is unauthenticated: no Authorization header is ever
# sent, and no key is ever minted.
req() { local m=$1 p=$2 b=${3:-}
  curl -sS -m 20 -w '\n%{http_code}' -X "$m" -H 'content-type: application/json' \
    -H "Idempotency-Key: haam-$RANDOM" ${b:+-d "$b"} "$BASE$p"; }
code() { req "$@" | tail -1; }
body() { req "$@" | sed '$d'; }
settle() { # settle <instance> <node type> -> fresh|failed
  local iid=$1 nt=$2 s
  while :; do
    s=$(body GET "/v1/observability/nodes/$iid/$nt" | jq -r '.run_summary as $r |
      if $r == null then "in-flight" elif $r.failed_count > 0 then "failed"
      elif $r.fresh_count > 0 and $r.active_count == 0 and $r.pending_count == 0 then "fresh"
      else "in-flight" end')
    [ "$s" = in-flight ] || { echo "$s"; return; }
    sleep 0.5
  done
}
key_count() { body GET /v1/auth/keys | jq '[.. | objects | select(has("id") and has("name"))] | length'; }

note "== build the operator's local binary =="
cp -r "$PEERSRC" "$WORK/peer"
sed "s|RIMSKY_PROTOCOLS_PATH|$ROOT/lib/protocols|" "$WORK/peer/go.mod.tmpl" > "$WORK/peer/go.mod"
rm "$WORK/peer/go.mod.tmpl"
( cd "$WORK/peer" && GOFLAGS=-mod=mod GOPROXY=off GOSUMDB=off go build -o "$WORK/peer-host" . ) || exit 1
check "the local binary built" yes yes

note
note "== a fresh deployment with no credentials at all =="
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
  -v "$WORK/rimsky.yml:/etc/rimsky/rimsky.yml:ro" "rimsky-all-in-one:$RIMSKY_IMAGE_TAG" >/dev/null || exit 1
until [ "$(code GET /v1/health)" = 200 ] || ! docker ps --format '{{.Names}}' | grep -q "^$STACK\$"; do sleep 0.5; done
check "the deployment reports anonymous mode" anonymous "$(body GET /v1/auth/status | jq -r .mode)"
check "no api-key exists" 0 "$(key_count)"

note
note "== the agent connects carrying no api-key =="
mkdir -p "$STATE"
# No --api-key and no RIMSKY_API_KEY: the agent registers anonymously under the
# routing label the operator chose.
env -u RIMSKY_API_KEY RIMSKY_ALLOW_PLAINTEXT_ENROLLMENT=1 HOME="$WORK" "$CLI" agent start \
  --proxy "127.0.0.1:$PROXYPORT" --label "$AGENT_LABEL" \
  --state-dir "$STATE" --identity-file "$STATE/identity.json" --listen 127.0.0.1:0 >"$WORK/start.out" 2>&1
check "the agent connected without credentials" 0 "$?"
sed 's/^/    /' "$WORK/start.out"
check "the agent adopted the operator's routing label" yes \
  "$(grep -q "$AGENT_LABEL" "$STATE/identity.json" 2>/dev/null && echo yes || echo no)"
check "minting no key was needed to connect" 0 "$(key_count)"

note
note "== registering and dispatching, all unauthenticated =="
SPEC='{"tag":"haam","spec":{"name":"haam","version":"1","late_bind_services":["devsvc"],"nodes":[{"type":"worker","executor":"devsvc"}]}}'
TPL=$(body POST /v1/templates "$SPEC" | jq -r '.template_id // empty')
check "the template registered without a token" yes "$([ -n "$TPL" ] && echo yes || echo no)"
check "the template deployed without a token" 200 "$(code POST "/v1/templates/$TPL/deploy" '{}')"

NOTARGET=$(body POST /v1/instances "$(printf '{"template":"%s","instance_key":"ck-haam-notarget","params":{},"service_bindings":{"devsvc":{"path":"%s"}}}' "$TPL" "$WORK/peer-host")")
note "    creating an instance with no target agent says: $(printf '%s' "$NOTARGET" | jq -c .)"
check "an anonymous-mode instance must name a target agent" yes \
  "$(printf '%s' "$NOTARGET" | grep -q 'target_agent' && echo yes || echo no)"

CREATE=$(printf '{"template":"%s","instance_key":"ck-haam","params":{},"target_agent":"%s","service_bindings":{"devsvc":{"path":"%s","env":{"PEER_LABEL":"anon-dev-binary"}}}}' \
  "$TPL" "$AGENT_LABEL" "$WORK/peer-host")
IID=$(body POST /v1/instances "$CREATE" | jq -r '.instance_id // empty')
check "the instance created without a token" yes "$([ -n "$IID" ] && echo yes || echo no)"
[ -n "$IID" ] || { note "EXPERIMENT FAIL"; exit 1; }
check "the deployment stamped the instance with the operator's agent" "$AGENT_LABEL" \
  "$(body GET "/v1/instances/$IID" | jq -r '[.. | objects | .target_routing_identity? // empty] | first // empty')"
code POST "/v1/instances/$IID/messages" '{"type":""}' >/dev/null

note "waiting for the dispatch to settle (blocks until it does)"
check "the dispatch settled on the operator's local binary" fresh "$(settle "$IID" worker)"
check "the local binary served it" anon-dev-binary \
  "$(body GET "/v1/observability/nodes/$IID/worker" | jq -r '[.. | objects | .served_by? // empty] | first // empty')"
check "the agent spawned the binary once" 1 "$(grep -c 'spawned child' "$STATE/agent.log")"

note
note "== an instance aimed at an agent nobody runs is not served here =="
GHOST=$(body POST /v1/instances "$(printf '{"template":"%s","instance_key":"ck-haam-ghost","params":{},"target_agent":"quiet-badger","service_bindings":{"devsvc":{"path":"%s"}}}' "$TPL" "$WORK/peer-host")" | jq -r '.instance_id // empty')
check "the instance aimed elsewhere was created" yes "$([ -n "$GHOST" ] && echo yes || echo no)"
code POST "/v1/instances/$GHOST/messages" '{"type":""}' >/dev/null
note "waiting for the unrouted dispatch to settle (blocks until it does)"
check "it did not succeed on the connected agent" failed "$(settle "$GHOST" worker)"
check "and the connected agent spawned nothing more" 1 "$(grep -c 'spawned child' "$STATE/agent.log")"

note
note "== nothing was ever minted =="
check "the deployment is still in anonymous mode" anonymous "$(body GET /v1/auth/status | jq -r .mode)"
check "there is still no api-key" 0 "$(key_count)"

note
if [ "$fail" = 0 ]; then note "EXPERIMENT PASS"; else note "EXPERIMENT FAIL"; fi
exit "$fail"
