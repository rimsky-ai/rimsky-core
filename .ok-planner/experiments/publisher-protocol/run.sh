#!/usr/bin/env bash
# Experiment: publisher-protocol
#
# A third-party publisher (peer/, its own Go module whose only rimsky
# requirement is the protocols module, built the same way as the
# permissive-peer-build experiment's executor) is wired into a stack as an
# ordinary publisher and then:
#
#   plugs in            -> its advertised kind gates what templates may declare
#   is subscribed to    -> rimsky calls Subscribe once when an instance mounts
#   feeds a workflow    -> the message it posts wakes the subscribing node
#   survives a restart  -> after rimsky restarts, its subscription is not
#                          re-issued, and the next message it feeds still lands
#   is released         -> terminating the instance unsubscribes it

set -u
cd "$(dirname "$0")"
ROOT=$(cd ../../.. && pwd)
: "${RIMSKY_IMAGE_TAG:?export RIMSKY_IMAGE_TAG=src-<tree hash> first}"
NET=exp-pub-net
STACK=exp-pub-stack
PEER=exp-pub-peer
PORT=${PORT:-19317}
PEER_HTTP=${PEER_HTTP:-19327}
WORK=$(mktemp -d)
BASE="http://127.0.0.1:$PORT"
PEERURL="http://127.0.0.1:$PEER_HTTP"

fail=0
note() { printf '%s\n' "$*"; }
check() {
  if [ "$2" = "$3" ]; then printf 'PASS  %-64s %s\n' "$1" "$3"
  else printf 'FAIL  %-64s expected [%s] got [%s]\n' "$1" "$2" "$3"; fail=1; fi
}
contains() {
  case "$3" in *"$2"*) printf 'PASS  %-64s %s\n' "$1" "$2";;
  *) printf 'FAIL  %-64s expected to contain [%s] got [%s]\n' "$1" "$2" "$3"; fail=1;; esac
}
cleanup() {
  docker rm -f "$STACK" "$PEER" >/dev/null 2>&1
  docker network rm "$NET" >/dev/null 2>&1
  rm -rf "$WORK"
}
trap cleanup EXIT

req() { local m=$1 p=$2 b=${3:-}
  curl -sS -m 20 -w '\n%{http_code}' -X "$m" -H 'content-type: application/json' \
    -H "Idempotency-Key: pb-$RANDOM$RANDOM" ${b:+-d "$b"} "$BASE$p"; }
code() { req "$@" | tail -1; }
body() { req "$@" | sed '$d'; }
state() { curl -sS -m 10 "$PEERURL/state"; }
counts() { state | jq -r ".counts.$1 // 0"; }

note "== build the publisher and bring up a stack that knows its endpoint =="
cp -r peer "$WORK/peer"
sed "s|RIMSKY_PROTOCOLS_PATH|$ROOT/lib/protocols|" "$WORK/peer/go.mod.tmpl" > "$WORK/peer/go.mod"
rm "$WORK/peer/go.mod.tmpl"
ARCH=$(docker info --format '{{.Architecture}}' | sed 's/aarch64/arm64/;s/x86_64/amd64/')
( cd "$WORK/peer" && GOWORK=off GOOS=linux GOARCH="$ARCH" CGO_ENABLED=0 GOFLAGS=-mod=mod GOPROXY=off GOSUMDB=off go build -o "$WORK/peer-linux" . ) \
  && check "the publisher builds against the protocols module alone" yes yes \
  || { check "the publisher builds against the protocols module alone" yes no; note "EXPERIMENT FAIL"; exit 1; }

cat > "$WORK/rimsky.yml" <<'YAML'
persistence:
  driver: sqlite
  sqlite:
    path: /var/lib/rimsky/state.db
claim_producers: {}
named_locks: {}
executors: {}
publishers:
  ticker:
    endpoint: "publisher:9500"
    protocols: ["publisher"]
YAML
docker network create "$NET" >/dev/null 2>&1
docker rm -f "$STACK" "$PEER" >/dev/null 2>&1
docker run -d --name "$PEER" --network "$NET" --network-alias publisher -p "$PEER_HTTP:9501" \
  -e PEER_PORT=9500 -e PEER_HTTP_PORT=9501 -e RIMSKY_ENDPOINT=http://rimsky:8080 \
  -v "$WORK/peer-linux:/peer:ro" alpine:latest /peer >/dev/null || exit 1
docker run -d --name "$STACK" --network "$NET" --network-alias rimsky -p "$PORT:8080" \
  -v "$WORK/rimsky.yml:/etc/rimsky/rimsky.yml:ro" "rimsky-all-in-one:$RIMSKY_IMAGE_TAG" >/dev/null || exit 1
until [ "$(code GET /v1/health)" = 200 ]; do sleep 0.5; done
until curl -sf -m 5 "$PEERURL/state" >/dev/null; do sleep 0.5; done

note
note "== the publisher's advertised kind gates what a template may declare =="
BADSPEC='{"spec":{"name":"badpub","version":"1","messages":[{"type":"ext/tick","body_schema":{"type":"object","properties":{"n":{"type":"integer"}}}}],"publishers":[{"name":"ticker","kind":"not-advertised","config":{},"message_type":"ext/tick"}],"nodes":[{"type":"listener","kind":"attribute_passthrough","subscribes":[{"node":"ext/tick","type":"terminal/success","force_upstream_refresh":false}],"attributes":{"schema":{"type":"object","properties":{"seen":{"type":"integer","default":1}}}}}]}}'
contains "a kind the publisher never advertised is rejected" "publisher_unadvertised_kind" \
  "$(body POST /v1/templates/validate "$BADSPEC" | jq -r '[.validation_errors[]?.msg]|join(" ~ ")')"

note
note "== a template that declares the publisher's own kind =="
SPEC='{"tag":"pub","spec":{"name":"pub","version":"1",
 "messages":[{"type":"ext/tick","body_schema":{"type":"object","properties":{"n":{"type":"integer"}}}}],
 "publishers":[{"name":"ticker","kind":"tick","config":{"label":"probe"},"message_type":"ext/tick"}],
 "nodes":[
  {"type":"listener","kind":"attribute_passthrough","subscribes":[{"node":"ext/tick","type":"terminal/success","force_upstream_refresh":false}],"attributes":{"schema":{"type":"object","properties":{"seen":{"type":"integer","default":1}}}}}
]}}'
REG=$(body POST /v1/templates "$SPEC")
TPL=$(printf '%s' "$REG" | jq -r '.template_id // empty')
check "template registered" yes "$([ -n "$TPL" ] && echo yes || echo no)"
[ -n "$TPL" ] || { note "register response: $REG"; note "EXPERIMENT FAIL"; exit 1; }
check "template deployed" 200 "$(code POST "/v1/templates/$TPL/deploy" '{}')"
IID=$(body POST /v1/instances "{\"template\":\"$TPL\",\"instance_key\":\"pub-1\",\"params\":{},\"target_agent\":\"pub-agent\"}" | jq -r '.instance_id // empty')
check "instance created" yes "$([ -n "$IID" ] && echo yes || echo no)"
[ -n "$IID" ] || { note "EXPERIMENT FAIL"; exit 1; }

note "waiting for rimsky to subscribe the publisher (blocks until it does)"
until [ "$(state | jq '[.subscriptions[]?]|length')" -ge 1 ]; do sleep 0.5; done
SUBID=$(state | jq -r '.subscriptions[0].publisher_subscription_id')
check "rimsky called Subscribe exactly once" 1 "$(counts subscribe_calls)"
check "the subscription names the instance it is for" "$IID" "$(state | jq -r '.subscriptions[0].instance_id')"
check "the subscription carries the template's kind" tick "$(state | jq -r '.subscriptions[0].kind')"
check "the subscription carries the template's message type" "ext/tick" "$(state | jq -r '.subscriptions[0].message_type')"
check "the subscription carries the template's resolved config" '{"label":"probe"}' "$(state | jq -r '.subscriptions[0].config')"

note
note "== the publisher feeds a message into the workflow =="
runs() { body GET "/v1/observability/nodes/$IID/listener" | jq '((.run_summary.fresh_count // 0) + (.run_summary.failed_count // 0))'; }
check "the publisher posted its message" 1 "$(curl -sS -m 10 "$PEERURL/publish" | jq '.sent')"
note "waiting for the subscribing node to run on that message (blocks until it does)"
until [ "$(runs)" -ge 1 ]; do sleep 0.5; done
check "the subscribing node ran on the publisher's message" 1 "$(runs)"
check "the message is attributed to the publisher rimsky knows" "ticker" \
  "$(body GET "/v1/instances/$IID/messages" | jq -r '[.messages[]?|select(.type=="ext/tick")|.sender]|first // empty')"
check "the message is attributed to a publisher, not an operator" "publisher" \
  "$(body GET "/v1/instances/$IID/messages" | jq -r '[.messages[]?|select(.type=="ext/tick")|.sender_kind]|first // empty')"
check "the publisher's own payload reached the workflow" 1 \
  "$(body GET "/v1/instances/$IID/messages" | jq '[.messages[]?|select(.type=="ext/tick")|.payload.n]|min')"

note
note "== restart rimsky; the subscription is not re-issued =="
LIST_BEFORE=$(counts list_calls)
docker restart "$STACK" >/dev/null
until [ "$(code GET /v1/health)" = 200 ]; do sleep 0.5; done
note "waiting for the restarted stack to reconcile against the publisher (blocks until it does)"
until [ "$(counts list_calls)" -gt "$LIST_BEFORE" ]; do sleep 0.5; done
check "rimsky asked the publisher what it already holds" yes \
  "$([ "$(counts list_calls)" -gt "$LIST_BEFORE" ] && echo yes || echo no)"
check "Subscribe was not called again after the restart" 1 "$(counts subscribe_calls)"
check "the publisher still holds the same subscription" "$SUBID" "$(state | jq -r '.subscriptions[0].publisher_subscription_id')"
check "Unsubscribe was not called across the restart" 0 "$(counts unsubscribe_calls)"

note
note "== and the publisher's next message still lands =="
check "the publisher posted a second message" 1 "$(curl -sS -m 10 "$PEERURL/publish" | jq '.sent')"
note "waiting for the subscribing node to run again (blocks until it does)"
until [ "$(runs)" -ge 2 ]; do sleep 0.5; done
check "the subscribing node ran on the second message too" 2 "$(runs)"

note
note "== terminating the instance releases the subscription =="
code POST "/v1/instances/$IID/terminate" '{"reason":"experiment done"}' >/dev/null
note "waiting for rimsky to unsubscribe (blocks until it does)"
until [ "$(counts unsubscribe_calls)" -ge 1 ]; do sleep 0.5; done
check "Unsubscribe was called for the instance's subscription" 1 "$(counts unsubscribe_calls)"
check "the publisher holds no subscriptions afterwards" 0 "$(state | jq '[.subscriptions[]?]|length')"

note
note "publisher log:"
docker logs "$PEER" 2>&1 | tail -8 | sed 's/^/    /'

note
if [ "$fail" = 0 ]; then note "EXPERIMENT PASS"; else note "EXPERIMENT FAIL"; fi
exit "$fail"
