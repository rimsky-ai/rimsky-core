#!/usr/bin/env bash
# Experiment: permissive-peer-build
#
# Builds a third-party rimsky peer (peer/) whose only rimsky dependency is the
# protocols module, then runs it against a real rimsky stack and exchanges the
# executor protocol's verbs with it:
#   Capabilities  -> the stack's discovery probe reaches the peer
#   Execute       -> a node dispatched to the peer settles success
#   Execute       -> a second node settles failed with the peer's own error class
#
# The licence claim is checked mechanically: every Go file in the module the peer
# depends on declares Apache-2.0, and the modules it does NOT depend on are the
# copyleft ones.

set -u
cd "$(dirname "$0")"
ROOT=$(cd ../../.. && pwd)
: "${RIMSKY_IMAGE_TAG:?export RIMSKY_IMAGE_TAG=src-<tree hash> first}"
NET=exp-ppb-net
STACK=exp-ppb-stack
PEER=exp-ppb-peer
PORT=${PORT:-18511}
WORK=$(mktemp -d)
BASE="http://127.0.0.1:$PORT"

fail=0
note() { printf '%s\n' "$*"; }
check() {
  if [ "$2" = "$3" ]; then printf 'PASS  %-62s %s\n' "$1" "$3"
  else printf 'FAIL  %-62s expected [%s] got [%s]\n' "$1" "$2" "$3"; fail=1; fi
}
cleanup() {
  docker rm -f "$STACK" "$PEER" >/dev/null 2>&1
  docker network rm "$NET" >/dev/null 2>&1
  rm -rf "$WORK"
}
trap cleanup EXIT

req() { local m=$1 p=$2 b=${3:-}
  curl -sS -m 15 -w '\n%{http_code}' -X "$m" -H 'content-type: application/json' \
    -H "Idempotency-Key: ppb-$RANDOM" ${b:+-d "$b"} "$BASE$p"; }
code() { req "$@" | tail -1; }
body() { req "$@" | sed '$d'; }

note "== build the peer =="
cp -r peer "$WORK/peer"
sed "s|RIMSKY_PROTOCOLS_PATH|$ROOT/lib/protocols|" "$WORK/peer/go.mod.tmpl" > "$WORK/peer/go.mod"
rm "$WORK/peer/go.mod.tmpl"
( cd "$WORK/peer" && GOFLAGS=-mod=mod GOPROXY=off GOSUMDB=off go build -o "$WORK/peer-host" . ) \
  && check "the peer builds" yes yes || check "the peer builds" yes no
( cd "$WORK/peer" && GOOS=linux GOARCH="$(docker info --format '{{.Architecture}}' | sed 's/aarch64/arm64/;s/x86_64/amd64/')" \
    CGO_ENABLED=0 GOFLAGS=-mod=mod GOPROXY=off GOSUMDB=off go build -o "$WORK/peer-linux" . ) \
  && check "the peer cross-builds for the stack's platform" yes yes || check "the peer cross-builds for the stack's platform" yes no

note
note "== the dependency boundary =="
RIMSKY_MODULES=$( cd "$WORK/peer" && GOFLAGS=-mod=mod GOPROXY=off go list -m all | awk '{print $1}' | grep '^github.com/rimsky-ai/' )
check "exactly one rimsky module in the build" "github.com/rimsky-ai/rimsky-core/lib/protocols" "$RIMSKY_MODULES"
RIMSKY_PKGS=$( cd "$WORK/peer" && GOFLAGS=-mod=mod GOPROXY=off go list -deps . | grep '^github.com/rimsky-ai/' | sed 's|/lib/protocols/.*|/lib/protocols/...|' | sort -u )
check "every rimsky package it links is under the protocols module" \
  "github.com/rimsky-ai/rimsky-core/lib/protocols/..." "$RIMSKY_PKGS"

APACHE=$(grep -rLE 'SPDX-License-Identifier: Apache-2.0|Licensed under the Apache License, Version 2.0' --include='*.go' "$ROOT/lib/protocols" 2>/dev/null | wc -l | tr -d ' ')
PROTO_GO=$(find "$ROOT/lib/protocols" -name '*.go' | wc -l | tr -d ' ')
check "every Go file in the protocols module declares Apache-2.0 (of $PROTO_GO)" 0 "$APACHE"
check "the root module it does not depend on is copyleft" yes \
  "$(grep -q 'AGPL-3.0-or-later' "$ROOT/lib/control/controlapi/app.go" && echo yes || echo no)"

note
note "== run it against a real rimsky stack =="
cat > "$WORK/rimsky.yml" <<'YAML'
persistence:
  driver: sqlite
  sqlite:
    path: /var/lib/rimsky/state.db
claim_producers: {}
named_locks: {}
executors:
  "third-party":
    transport: grpc
    endpoint: "peer:9400"
    protocols: ["executor"]
YAML
docker network create "$NET" >/dev/null 2>&1
docker rm -f "$STACK" "$PEER" >/dev/null 2>&1
docker run -d --name "$PEER" --network "$NET" --network-alias peer \
  -e PEER_PORT=9400 -e PEER_LABEL=third-party-peer \
  -v "$WORK/peer-linux:/peer:ro" alpine:latest /peer >/dev/null || exit 1
docker run -d --name "$STACK" --network "$NET" --network-alias rimsky-stack -p "$PORT:8080" \
  -v "$WORK/rimsky.yml:/etc/rimsky/rimsky.yml:ro" "rimsky-all-in-one:$RIMSKY_IMAGE_TAG" >/dev/null || exit 1
until [ "$(code GET /v1/health)" = 200 ]; do sleep 0.5; done

note "waiting for the stack's discovery probe to reach the peer (blocks until it does)"
until [ "$(body GET /v1/observability/executors/third-party | jq -r '.peer.reachability_status')" = reachable ]; do sleep 0.5; done
PEERVIEW=$(body GET /v1/observability/executors/third-party)
check "the stack reports the peer reachable" reachable "$(printf '%s' "$PEERVIEW" | jq -r .peer.reachability_status)"
check "the Capabilities verb returned the peer's declared error class" "third-party/refused" \
  "$(printf '%s' "$PEERVIEW" | jq -r '.peer.observability_capabilities.declared_error_classes|join(",")')"

SPEC='{"tag":"ppb","spec":{"name":"ppb","version":"1","nodes":[
  {"type":"okworker","executor":"third-party","attributes":{"schema":{"type":"object","properties":{"outcome":{"type":"string","default":"ok"},"echo":{"type":"string","default":"hello"}}}}},
  {"type":"failworker","executor":"third-party","attributes":{"schema":{"type":"object","properties":{"outcome":{"type":"string","default":"fail"},"echo":{"type":"string","default":"bye"}}}}}
]}}'
REG=$(body POST /v1/templates "$SPEC")
TPL=$(printf '%s' "$REG" | jq -r '.template_id // empty')
check "template registered" yes "$([ -n "$TPL" ] && echo yes || echo no)"
[ -n "$TPL" ] || note "register response: $REG"
check "template deployed" 200 "$(code POST "/v1/templates/$TPL/deploy" '{}')"

CREATE=$(printf '{"template":"%s","instance_key":"ck-ppb","params":{},"target_agent":"ppb-probe-agent"}' "$TPL")
IID=$(body POST /v1/instances "$CREATE" | jq -r '.instance_id // empty')
check "instance created" yes "$([ -n "$IID" ] && echo yes || echo no)"
[ -n "$IID" ] || { note "cannot continue without an instance"; note "EXPERIMENT FAIL"; exit 1; }
code POST "/v1/instances/$IID/messages" '{"type":""}' >/dev/null

settle() { # settle <node type> -> fresh|failed  (blocks until the node settles)
  local nt=$1 s
  while :; do
    s=$(body GET "/v1/observability/nodes/$IID/$nt" | jq -r '
      .run_summary as $r |
      if $r == null then "in-flight"
      elif $r.failed_count > 0 then "failed"
      elif $r.fresh_count > 0 and $r.active_count == 0 and $r.pending_count == 0 then "fresh"
      else "in-flight" end')
    [ "$s" = in-flight ] || { echo "$s"; return; }
    sleep 0.5
  done
}

note "waiting for both nodes to settle (blocks until they do)"
check "the peer's Execute verb settled the success node fresh" fresh "$(settle okworker)"
check "the peer's Execute verb settled the failure node failed" failed "$(settle failworker)"
OKNODE=$(body GET "/v1/observability/nodes/$IID/okworker")
note "success node latest_attributes: $(printf '%s' "$OKNODE" | jq -c .latest_attributes)"
check "the peer's success delta landed on the node" third-party-peer \
  "$(printf '%s' "$OKNODE" | jq -r '[.. | objects | .served_by? // empty] | first // empty')"
FAILNODE=$(body GET "/v1/observability/nodes/$IID/failworker")
check "the peer's own error class reached the record" "third-party/refused" \
  "$(printf '%s' "$FAILNODE" | jq -r '[.. | objects | .error_class? // empty] | map(select(. != "")) | unique | join(",")')"

note
note "peer container log:"
docker logs "$PEER" 2>&1 | tail -5 | sed 's/^/    /'

note
if [ "$fail" = 0 ]; then note "EXPERIMENT PASS"; else note "EXPERIMENT FAIL"; fi
exit "$fail"
