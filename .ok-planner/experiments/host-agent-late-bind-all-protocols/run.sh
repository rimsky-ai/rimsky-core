#!/usr/bin/env bash
# Experiment: host-agent-late-bind-all-protocols
#
# A template author runs a host-agent on the machine holding their local
# binaries, against a rimsky deployment running in containers, and declares
# bindings for the two peer protocols a late-bound binding can name. The run
# drives one instance per protocol and checks that each reaches a child the
# agent spawned from the author's declared path:
#   - executor        a node whose executor is the late-bound service
#   - claim_producer  a node whose claim names the late-bound service
#
# The deployment's own claim-producer entry for the proxy is exercised on its
# own as a control, so an unresolved late-bound claim is distinguishable from an
# unreachable proxy. A binding naming a path that does not exist is the fourth
# case, showing the agent's own failure reaching the node.
#
# peer/ is the local binary: its own Go module, depending only on rimsky's
# protocols module, serving the executor and claim-producer protocols and
# reporting the process facts that identify which child served a call.

set -u
cd "$(dirname "$0")"
ROOT=$(cd ../../.. && pwd)
: "${RIMSKY_IMAGE_TAG:?export RIMSKY_IMAGE_TAG=src-<tree hash> first}"
CLI="$ROOT/bin/rimsky"
NET=exp-halb-net
STACK=exp-halb-stack
PROXY=exp-halb-proxy
PORT=${PORT:-18938}
PROXYPORT=${PROXYPORT:-18939}
WORK=$(mktemp -d)
BASE="http://127.0.0.1:$PORT"
STATE="$WORK/agent"
AGENT_LABEL=tidy-heron

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
    -H "Idempotency-Key: halb-$RANDOM" ${b:+-d "$b"} "$BASE$p"; }
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
error_class() { # error_class <instance> <node type>
  body GET "/v1/observability/nodes/$1/$2" | jq -r '[.. | objects | .error_class? // empty] | first // empty'
}
register_deploy() { # register_deploy <spec json> -> template id
  local tpl
  tpl=$(body POST /v1/templates "$1" | jq -r '.template_id // empty')
  [ -n "$tpl" ] || return 1
  code POST "/v1/templates/$tpl/deploy" '{}' >/dev/null
  printf '%s' "$tpl"
}
launch() { # launch <template> <instance key> <bindings json> -> instance id
  local iid
  iid=$(body POST /v1/instances "$(printf '{"template":"%s","instance_key":"%s","params":{},"target_agent":"%s","service_bindings":%s}' \
    "$1" "$2" "$AGENT_LABEL" "$3")" | jq -r '.instance_id // empty')
  [ -n "$iid" ] || return 1
  code POST "/v1/instances/$iid/messages" '{"type":""}' >/dev/null
  printf '%s' "$iid"
}

note "== build the author's local binary =="
cp -r peer "$WORK/peer"
sed "s|RIMSKY_PROTOCOLS_PATH|$ROOT/lib/protocols|" "$WORK/peer/go.mod.tmpl" > "$WORK/peer/go.mod"
rm "$WORK/peer/go.mod.tmpl"
( cd "$WORK/peer" && GOFLAGS=-mod=mod GOPROXY=off GOSUMDB=off go build -o "$WORK/peer-host" . ) || exit 1
check "the local binary built outside any rimsky image" yes yes
BIND=$(printf '{"devsvc":{"path":"%s","env":{"PEER_LABEL":"local-dev-binary"}}}' "$WORK/peer-host")

note
note "== a containerised deployment that late-binds both protocols =="
cat > "$WORK/rimsky.yml" <<'YAML'
persistence:
  driver: sqlite
  sqlite:
    path: /var/lib/rimsky/state.db
named_locks: {}
late_bind_service_proxies:
  executor: agent-proxy
  claim_producer: agent-proxy
claim_producers:
  "agent-proxy":
    endpoint: "grpc://proxy:9090"
    protocols: ["claim_producer"]
    write_semantics_allowed: ["sync"]
executors:
  "agent-proxy":
    transport: grpc
    endpoint: "proxy:9090"
    protocols: ["executor", "lifecycle_subscriber"]
YAML
docker network create "$NET" >/dev/null 2>&1
docker rm -f "$STACK" "$PROXY" >/dev/null 2>&1
# The deployment handshakes its configured claim producer at boot, so the proxy
# it resolves that producer through has to be listening first.
docker run -d --name "$PROXY" --network "$NET" --network-alias proxy -p "$PROXYPORT:9090" \
  -e RIMSKY_PROXY_GRPC_PORT=9090 \
  -e RIMSKY_CONTROL_API_URL="http://rimsky-stack:8080" \
  "rimsky-host-agent-proxy:$RIMSKY_IMAGE_TAG" >/dev/null || exit 1
until nc -z 127.0.0.1 "$PROXYPORT" 2>/dev/null; do sleep 0.5; done
docker run -d --name "$STACK" --network "$NET" --network-alias rimsky-stack -p "$PORT:8080" \
  -v "$WORK/rimsky.yml:/etc/rimsky/rimsky.yml:ro" "rimsky-all-in-one:$RIMSKY_IMAGE_TAG" >/dev/null || exit 1
until [ "$(code GET /v1/health)" = 200 ] || ! docker ps --format '{{.Names}}' | grep -q "^$STACK\$"; do sleep 0.5; done
check "the deployment is up" 200 "$(code GET /v1/health)"
check "the deployment accepted a claim-producer entry for the proxy" yes \
  "$(body GET /v1/observability/claim-producers | grep -q 'agent-proxy' && echo yes || echo no)"

mkdir -p "$STATE"
RIMSKY_ALLOW_PLAINTEXT_ENROLLMENT=1 HOME="$WORK" "$CLI" agent start \
  --proxy "127.0.0.1:$PROXYPORT" --label "$AGENT_LABEL" \
  --state-dir "$STATE" --identity-file "$STATE/identity.json" --listen 127.0.0.1:0 >"$WORK/start.out" 2>&1
check "the author's agent connected to the deployment" 0 "$?"
sed 's/^/    /' "$WORK/start.out"

note
note "== the executor protocol through the late-bound binding =="
ETPL=$(register_deploy '{"tag":"halb-exec","spec":{"name":"halb-exec","version":"1","late_bind_services":["devsvc"],"nodes":[{"type":"worker","executor":"devsvc"}]}}')
check "the executor template deployed" yes "$([ -n "$ETPL" ] && echo yes || echo no)"
EIID=$(launch "$ETPL" ck-halb-exec "$BIND")
check "the instance bound the service to the author's own path" yes "$([ -n "$EIID" ] && echo yes || echo no)"
note "waiting for the executor dispatch to settle (blocks until it does)"
check "the executor node settled on the author's local binary" fresh "$(settle "$EIID" worker)"
ENODE=$(body GET "/v1/observability/nodes/$EIID/worker")
note "    node attributes: $(printf '%s' "$ENODE" | jq -c '.latest_attributes')"
check "the executor protocol reached the local binary" local-dev-binary \
  "$(printf '%s' "$ENODE" | jq -r '[.. | objects | .served_by? // empty] | first // empty')"
check "the local binary served the Execute verb" yes \
  "$(grep -q 'execute node=worker' "$STATE/agent.log" && echo yes || echo no)"
check "the agent spawned one child for it" 1 "$(grep -c 'spawned child' "$STATE/agent.log")"

note
note "== the claim-producer protocol through the late-bound binding =="
CTPL=$(register_deploy '{"tag":"halb-claim","spec":{"name":"halb-claim","version":"1","late_bind_services":["devsvc"],"nodes":[{"type":"claimer","executor":"devsvc","claim_producers":[{"name":"devsvc","selector":"@thing","intent":"rw","alias":"thing"}]}]}}')
check "the claim template deployed" yes "$([ -n "$CTPL" ] && echo yes || echo no)"
CIID=$(launch "$CTPL" ck-halb-claim "$BIND")
check "the instance bound the same service for the claim" yes "$([ -n "$CIID" ] && echo yes || echo no)"
note "waiting for the claim dispatch to settle (blocks until it does)"
CSETTLE=$(settle "$CIID" claimer)
note "    node error class: $(error_class "$CIID" claimer)"
check "the claim node settled on the author's local binary" fresh "$CSETTLE"
check "the local binary served the claim-producer Open verb" yes \
  "$(grep -q 'open claim=.*selector=@thing' "$STATE/agent.log" && echo yes || echo no)"
check "the local binary served the claim-producer Commit verb" yes \
  "$(grep -q 'commit claim=' "$STATE/agent.log" && echo yes || echo no)"

note
note "== control: the same proxy named directly, not through late binding =="
DTPL=$(register_deploy '{"tag":"halb-direct","spec":{"name":"halb-direct","version":"1","late_bind_services":["devsvc"],"nodes":[{"type":"claimer","executor":"devsvc","claim_producers":[{"name":"agent-proxy","selector":"@thing","intent":"rw","alias":"thing"}]}]}}')
DIID=$(launch "$DTPL" ck-halb-direct "$BIND")
note "waiting for the control dispatch to settle (blocks until it does)"
settle "$DIID" claimer >/dev/null
DCLASS=$(error_class "$DIID" claimer)
note "    naming the configured producer directly gives: ${DCLASS:-<no error>}"
check "the configured claim producer itself resolves" yes \
  "$([ "$DCLASS" = "acquire/unresolved_claim_producer" ] && echo no || echo yes)"

note
note "== a binding the agent will not run fails the dispatch =="
BIID=$(launch "$ETPL" ck-halb-bad "$(printf '{"devsvc":{"path":"%s"}}' "$WORK/no-such-binary")")
note "waiting for the bad binding to settle (blocks until it does)"
check "the dispatch failed instead of succeeding elsewhere" failed "$(settle "$BIID" worker)"
check "and it failed with the agent's own spawn error" spawn_failed "$(error_class "$BIID" worker)"

note
note "the local binary's own log:"
grep -E '(open|commit|release|execute|peer) ' "$STATE/agent.log" | sed 's/^/    /'

note
if [ "$fail" = 0 ]; then note "EXPERIMENT PASS"; else note "EXPERIMENT FAIL"; fi
exit "$fail"
