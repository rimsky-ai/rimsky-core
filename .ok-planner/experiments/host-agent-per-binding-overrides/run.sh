#!/usr/bin/env bash
# Experiment: host-agent-per-binding-overrides
#
# A template author declares two late-bound bindings that run the same binary
# under different configuration, and a third whose binary is slow to come up.
# The run checks that each of the four things a binding can override reaches the
# spawned process:
#   - env       each child sees the variables its own binding declared
#   - args      each child receives the argument vector its own binding declared
#   - cwd       each child runs in the working directory its own binding declared
#   - timeout   the same slow binary fails to spawn under a short declared
#               timeout and spawns under a long one, with nothing else changed
#
# The binary is the local service built for host-agent-late-bind-all-protocols;
# it reports its pid, its argument vector, its working directory and every
# PEER_-prefixed variable in its environment as the node's attributes.

set -u
cd "$(dirname "$0")"
ROOT=$(cd ../../.. && pwd)
: "${RIMSKY_IMAGE_TAG:?export RIMSKY_IMAGE_TAG=src-<tree hash> first}"
CLI="$ROOT/bin/rimsky"
PEERSRC="$ROOT/.ok-planner/experiments/host-agent-late-bind-all-protocols/peer"
NET=exp-hapb-net
STACK=exp-hapb-stack
PROXY=exp-hapb-proxy
PORT=${PORT:-18941}
PROXYPORT=${PROXYPORT:-18942}
WORK=$(mktemp -d)
BASE="http://127.0.0.1:$PORT"
STATE="$WORK/agent"
AGENT_LABEL=brisk-otter

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
    -H "Idempotency-Key: hapb-$RANDOM" ${b:+-d "$b"} "$BASE$p"; }
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
attr() { # attr <instance> <node type> <jq path expression>
  body GET "/v1/observability/nodes/$1/$2" | jq -rc "[.. | objects | select(has(\"served_by\")) | $3] | first // empty"
}

note "== build the binary both bindings will run =="
cp -r "$PEERSRC" "$WORK/peer"
sed "s|RIMSKY_PROTOCOLS_PATH|$ROOT/lib/protocols|" "$WORK/peer/go.mod.tmpl" > "$WORK/peer/go.mod"
rm "$WORK/peer/go.mod.tmpl"
( cd "$WORK/peer" && GOFLAGS=-mod=mod GOPROXY=off GOSUMDB=off go build -o "$WORK/peer-host" . ) || exit 1
mkdir -p "$WORK/dir-alpha" "$WORK/dir-beta"
DIR_A=$(cd "$WORK/dir-alpha" && pwd -P)
DIR_B=$(cd "$WORK/dir-beta" && pwd -P)
check "the binary built" yes yes
note "    working directory for the first binding:  $DIR_A"
note "    working directory for the second binding: $DIR_B"

note
note "== a deployment with a host-agent proxy and a connected agent =="
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
mkdir -p "$STATE"
RIMSKY_ALLOW_PLAINTEXT_ENROLLMENT=1 HOME="$WORK" "$CLI" agent start \
  --proxy "127.0.0.1:$PROXYPORT" --label "$AGENT_LABEL" \
  --state-dir "$STATE" --identity-file "$STATE/identity.json" --listen 127.0.0.1:0 >"$WORK/start.out" 2>&1
check "the agent connected" 0 "$?"

note
note "== two bindings, one binary, different configuration =="
SPEC='{"tag":"hapb","spec":{"name":"hapb","version":"1","late_bind_services":["svc-a","svc-b"],"nodes":[
 {"type":"alpha","executor":"svc-a"},{"type":"beta","executor":"svc-b"}]}}'
TPL=$(body POST /v1/templates "$SPEC" | jq -r '.template_id // empty')
check "the template declaring two late-bound services registered" yes "$([ -n "$TPL" ] && echo yes || echo no)"
check "the template deployed" 200 "$(code POST "/v1/templates/$TPL/deploy" '{}')"

cat > "$WORK/create.json" <<JSON
{"template":"$TPL","instance_key":"ck-hapb","params":{},"target_agent":"$AGENT_LABEL",
 "service_bindings":{
   "svc-a":{"path":"$WORK/peer-host","args":["--flavour","vanilla","--scoops","2"],
            "cwd":"$DIR_A","env":{"PEER_LABEL":"alpha-binding","PEER_FLAVOUR":"vanilla"}},
   "svc-b":{"path":"$WORK/peer-host","args":["--flavour","chocolate"],
            "cwd":"$DIR_B","env":{"PEER_LABEL":"beta-binding","PEER_FLAVOUR":"chocolate"}}}}
JSON
IID=$(body POST /v1/instances "$(cat "$WORK/create.json")" | jq -r '.instance_id // empty')
check "the instance declared both bindings" yes "$([ -n "$IID" ] && echo yes || echo no)"
[ -n "$IID" ] || { note "EXPERIMENT FAIL"; exit 1; }
code POST "/v1/instances/$IID/messages" '{"type":""}' >/dev/null

note "waiting for both dispatches to settle (blocks until they do)"
check "the first binding's node settled" fresh "$(settle "$IID" alpha)"
check "the second binding's node settled" fresh "$(settle "$IID" beta)"

note "what each spawned child reported about itself:"
body GET "/v1/observability/nodes/$IID/alpha" | jq -c '[.. | objects | select(has("served_by"))] | first' | sed 's/^/    alpha: /'
body GET "/v1/observability/nodes/$IID/beta" | jq -c '[.. | objects | select(has("served_by"))] | first' | sed 's/^/    beta:  /'

check "the first child carries its binding's environment" vanilla "$(attr "$IID" alpha '.env.PEER_FLAVOUR')"
check "the second child carries its binding's environment" chocolate "$(attr "$IID" beta '.env.PEER_FLAVOUR')"
check "the first child carries its binding's label" alpha-binding "$(attr "$IID" alpha '.served_by')"
check "the second child carries its binding's label" beta-binding "$(attr "$IID" beta '.served_by')"
check "the first child received its binding's arguments" '["--flavour","vanilla","--scoops","2"]' "$(attr "$IID" alpha '.args')"
check "the second child received its binding's arguments" '["--flavour","chocolate"]' "$(attr "$IID" beta '.args')"
check "the first child ran in its binding's directory" "$DIR_A" "$(attr "$IID" alpha '.cwd')"
check "the second child ran in its binding's directory" "$DIR_B" "$(attr "$IID" beta '.cwd')"
check "the two bindings ran as two separate processes" 2 \
  "$(printf '%s\n%s\n' "$(attr "$IID" alpha '.pid')" "$(attr "$IID" beta '.pid')" | sort -u | wc -l | tr -d ' ')"

note
note "== the same slow binary under a short and a long declared timeout =="
SLOWSPEC='{"tag":"hapb-slow","spec":{"name":"hapb-slow","version":"1","late_bind_services":["svc-c"],"nodes":[
 {"type":"slow","executor":"svc-c"}]}}'
STPL=$(body POST /v1/templates "$SLOWSPEC" | jq -r '.template_id // empty')
check "the slow-binary template deployed" 200 "$(code POST "/v1/templates/$STPL/deploy" '{}')"

slow_case() { # slow_case <instance key> <timeout seconds> -> fresh|failed
  local key=$1 secs=$2 iid
  cat > "$WORK/create-$key.json" <<JSON
{"template":"$STPL","instance_key":"$key","params":{},"target_agent":"$AGENT_LABEL",
 "service_bindings":{"svc-c":{"path":"$WORK/peer-host","timeout_seconds":$secs,
   "env":{"PEER_LABEL":"slow-binding","PEER_STARTUP_DELAY_SECONDS":"20"}}}}
JSON
  iid=$(body POST /v1/instances "$(cat "$WORK/create-$key.json")" | jq -r '.instance_id // empty')
  printf '%s' "$iid" > "$WORK/iid-$key"
  code POST "/v1/instances/$iid/messages" '{"type":""}' >/dev/null
  settle "$iid" slow
}
note "waiting for the short-timeout dispatch to settle (blocks until it does)"
check "a two-second declared timeout fails the slow binary" failed "$(slow_case ck-short 2)"
SHORT=$(cat "$WORK/iid-ck-short")
check "and it fails with the agent's spawn error" yes \
  "$(body GET "/v1/observability/nodes/$SHORT/slow" | grep -q 'spawn_failed' && echo yes || echo no)"
note "waiting for the long-timeout dispatch to settle (blocks until it does)"
check "a sixty-second declared timeout runs the same binary" fresh "$(slow_case ck-long 60)"
LONG=$(cat "$WORK/iid-ck-long")
check "and the slow child served the dispatch" slow-binding "$(attr "$LONG" slow '.served_by')"

note
if [ "$fail" = 0 ]; then note "EXPERIMENT PASS"; else note "EXPERIMENT FAIL"; fi
exit "$fail"
