#!/usr/bin/env bash
# Experiment: lifecycle-subscriber-author
#
# A third-party lifecycle subscriber (peer/, its own Go module whose only
# rimsky requirement is the protocols module, built the same way as the
# permissive-peer-build experiment's peer) is registered by declaring the
# lifecycle-subscriber protocol alongside its executor role, and then the run
# walks an instance through every lifecycle transition the story names,
# checking after each control-API call that the callback has already landed —
# which is what "synchronously" means from the caller's side.
#
#   register / deploy / undeploy / deregister a template
#   create / terminate an instance
#   a run scope reaching terminal
#
# and that each callback carried the context the story names.

set -u
cd "$(dirname "$0")"
ROOT=$(cd ../../.. && pwd)
: "${RIMSKY_IMAGE_TAG:?export RIMSKY_IMAGE_TAG=src-<tree hash> first}"
NET=exp-lifecycle-net
STACK=exp-lifecycle-stack
PEER=exp-lifecycle-peer
PORT=${PORT:-19318}
PEER_HTTP=${PEER_HTTP:-19328}
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
  docker rm -f "$STACK" "$PEER" >/dev/null 2>&1
  docker network rm "$NET" >/dev/null 2>&1
  rm -rf "$WORK"
}
trap cleanup EXIT

KEY=""
req() { local m=$1 p=$2 b=${3:-}
  curl -sS -m 20 -w '\n%{http_code}' -X "$m" -H 'content-type: application/json' \
    ${KEY:+-H "Authorization: Bearer $KEY"} \
    -H "Idempotency-Key: lc-$RANDOM$RANDOM" ${b:+-d "$b"} "$BASE$p"; }
code() { req "$@" | tail -1; }
body() { req "$@" | sed '$d'; }
seen() { curl -sS -m 10 "$PEERURL/state"; }
count_of() { seen | jq --arg c "$1" '[.[]|select(.callback==$c)]|length'; }
payload_of() { seen | jq -r --arg c "$1" --arg f "$2" '[.[]|select(.callback==$c)|.payload[$f]]|last // empty'; }

note "== build the subscriber and register it as a peer of the deployment =="
cp -r peer "$WORK/peer"
sed "s|RIMSKY_PROTOCOLS_PATH|$ROOT/lib/protocols|" "$WORK/peer/go.mod.tmpl" > "$WORK/peer/go.mod"
rm "$WORK/peer/go.mod.tmpl"
ARCH=$(docker info --format '{{.Architecture}}' | sed 's/aarch64/arm64/;s/x86_64/amd64/')
( cd "$WORK/peer" && GOWORK=off GOOS=linux GOARCH="$ARCH" CGO_ENABLED=0 GOFLAGS=-mod=mod GOPROXY=off GOSUMDB=off go build -o "$WORK/peer-linux" . ) \
  && check "the subscriber builds against the protocols module alone" yes yes \
  || { check "the subscriber builds against the protocols module alone" yes no; note "EXPERIMENT FAIL"; exit 1; }

cat > "$WORK/rimsky.yml" <<'YAML'
persistence:
  driver: sqlite
  sqlite:
    path: /var/lib/rimsky/state.db
claim_producers: {}
named_locks: {}
executors:
  "watcher":
    transport: grpc
    endpoint: "peer:9600"
    protocols: ["executor", "lifecycle_subscriber"]
YAML
docker network create "$NET" >/dev/null 2>&1
docker rm -f "$STACK" "$PEER" >/dev/null 2>&1
docker run -d --name "$PEER" --network "$NET" --network-alias peer -p "$PEER_HTTP:9601" \
  -e PEER_PORT=9600 -e PEER_HTTP_PORT=9601 -e PEER_LABEL=watcher \
  -v "$WORK/peer-linux:/peer:ro" alpine:latest /peer >/dev/null || exit 1
docker run -d --name "$STACK" --network "$NET" -p "$PORT:8080" \
  -v "$WORK/rimsky.yml:/etc/rimsky/rimsky.yml:ro" "rimsky-all-in-one:$RIMSKY_IMAGE_TAG" >/dev/null || exit 1
until [ "$(code GET /v1/health)" = 200 ]; do sleep 0.5; done
until curl -sf -m 5 "$PEERURL/state" >/dev/null; do sleep 0.5; done
until [ "$(body GET /v1/observability/executors/watcher | jq -r '.peer.reachability_status')" = reachable ]; do sleep 0.5; done
check "the subscriber is a live peer of the deployment" reachable \
  "$(body GET /v1/observability/executors/watcher | jq -r '.peer.reachability_status')"
check "no callback has fired before anything happened" 0 "$(seen | jq 'length')"
KEY=$(body POST /v1/auth/keys '{"name":"owner","permissions":[{"action":"*"}]}' | jq -r '.plaintext // empty')
check "an owner key was minted, so the callbacks have an owner to name" yes \
  "$([ -n "$KEY" ] && echo yes || echo no)"

note
note "== template registered =="
SPEC='{"tag":"lifecycle","spec":{"name":"lifecycle","version":"1","nodes":[
 {"type":"worker","executor":"watcher","attributes":{"schema":{"type":"object","properties":{"outcome":{"type":"string","default":"ok"},"echo":{"type":"string","default":"lc"}}}}}
]}}'
REG=$(body POST /v1/templates "$SPEC")
TPL=$(printf '%s' "$REG" | jq -r '.template_id // empty')
[ -n "$TPL" ] || { note "register response: $REG"; note "EXPERIMENT FAIL"; exit 1; }
check "OnTemplateRegistered had already fired when register returned" 1 "$(count_of OnTemplateRegistered)"
check "it carried the template hash" "$TPL" "$(payload_of OnTemplateRegistered template_hash)"
check "it carried the template spec" lifecycle "$(payload_of OnTemplateRegistered spec_name)"

note
note "== template deployed =="
check "deploy accepted" 200 "$(code POST "/v1/templates/$TPL/deploy" '{}')"
check "OnTemplateDeployed had already fired when deploy returned" 1 "$(count_of OnTemplateDeployed)"
check "it carried the template hash" "$TPL" "$(payload_of OnTemplateDeployed template_hash)"
check "it carried the deployment's tags" '["lifecycle"]' "$(seen | jq -c '[.[]|select(.callback=="OnTemplateDeployed")|.payload.tags]|last')"

note
note "== instance created =="
IID=$(body POST /v1/instances "{\"template\":\"$TPL\",\"instance_key\":\"lifecycle-1\",\"params\":{\"who\":\"probe\"},\"service_bindings\":{\"helper\":\"grpc://helper:9000\"},\"target_agent\":\"lifecycle-agent\"}" | jq -r '.instance_id // empty')
[ -n "$IID" ] || { note "EXPERIMENT FAIL"; exit 1; }
check "OnInstanceCreated had already fired when create returned" 1 "$(count_of OnInstanceCreated)"
check "it carried the instance id" "$IID" "$(payload_of OnInstanceCreated instance_id)"
check "it carried the template hash" "$TPL" "$(payload_of OnInstanceCreated template_hash)"
check "it carried the instance key" "lifecycle-1" "$(payload_of OnInstanceCreated instance_key)"
check "it carried the params" '{"who":"probe"}' "$(payload_of OnInstanceCreated params)"
check "it carried the service bindings the caller supplied" '{"helper":"grpc://helper:9000"}' \
  "$(payload_of OnInstanceCreated service_bindings)"
check "it carried the owner key that created the instance" yes \
  "$([ -n "$(payload_of OnInstanceCreated owner_api_key_id)" ] && echo yes || echo no)"
check "it carried the routing identity the instance was created under" yes \
  "$([ -n "$(payload_of OnInstanceCreated target_routing_identity)" ] && echo yes || echo no)"

note
note "== a run scope reaches terminal =="
code POST "/v1/instances/$IID/messages" '{"type":""}' >/dev/null
note "waiting for the run scope to reach terminal (blocks until it does)"
until [ "$(count_of OnRunScopeTerminal)" -ge 1 ]; do sleep 0.5; done
check "OnRunScopeTerminal fired" 1 "$(count_of OnRunScopeTerminal)"
check "it carried the run-scope id" yes \
  "$([ -n "$(payload_of OnRunScopeTerminal run_scope_id)" ] && echo yes || echo no)"
check "it carried the instance the scope belonged to" "$IID" "$(payload_of OnRunScopeTerminal instance_id)"
check "it carried the terminal reason" yes \
  "$([ -n "$(payload_of OnRunScopeTerminal terminal_reason)" ] && echo yes || echo no)"
note "terminal reason: $(payload_of OnRunScopeTerminal terminal_reason)"

note
note "== instance terminated =="
check "terminate accepted" 200 "$(code POST "/v1/instances/$IID/terminate" '{"reason":"experiment done"}')"
check "the instance is terminal before it is deleted" yes \
  "$(body GET "/v1/instances/$IID" | jq -r 'if (.terminated_at//"")!="" then "yes" else "no" end')"
check "delete accepted" 200 "$(code DELETE "/v1/instances/$IID")"
check "OnInstanceTerminated had already fired when delete returned" 1 "$(count_of OnInstanceTerminated)"
check "it carried the instance id" "$IID" "$(payload_of OnInstanceTerminated instance_id)"
check "it carried the template hash" "$TPL" "$(payload_of OnInstanceTerminated template_hash)"
check "it carried when the instance terminated" true "$(payload_of OnInstanceTerminated terminated_at_is_set)"

note
note "== template undeployed and deregistered =="
check "undeploy accepted" 200 "$(code POST "/v1/templates/$TPL/undeploy" '{}')"
check "OnTemplateUndeployed had already fired when undeploy returned" 1 "$(count_of OnTemplateUndeployed)"
check "it carried the template hash" "$TPL" "$(payload_of OnTemplateUndeployed template_hash)"
DEL=$(code DELETE "/v1/templates/$TPL")
check "deregister accepted" 200 "$DEL"
check "OnTemplateDeregistered had already fired when delete returned" 1 "$(count_of OnTemplateDeregistered)"
check "it carried the template hash" "$TPL" "$(payload_of OnTemplateDeregistered template_hash)"

note
note "== all seven callbacks, in the order the transitions happened =="
note "$(seen | jq -r '[.[].callback]|join(" -> ")')"
check "every one of the seven callbacks fired" 7 "$(seen | jq '[.[].callback]|unique|length')"

note
if [ "$fail" = 0 ]; then note "EXPERIMENT PASS"; else note "EXPERIMENT FAIL"; fi
exit "$fail"
