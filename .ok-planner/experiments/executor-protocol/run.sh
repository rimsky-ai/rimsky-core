#!/usr/bin/env bash
# Experiment: executor-protocol
#
# A third-party executor (peer/, reused from the permissive-peer-build
# experiment and extended) implements the unary Execute verb and the
# executor-observability handshake: a closed expected-attributes schema,
# declared tags, and declared error classes. Against a real rimsky stack the
# run then exercises what the story says rimsky does with all of it:
#
#   discovery at startup            -> the stack reports the peer's advertisement
#   attribute-schema validation     -> templates conflicting with it are rejected
#   dispatch + settling outcomes    -> success, error and park each land
#   error routing by declared class -> one class gives up, another retries
#   declared tags                   -> gate a tag-filtered subscription
#
# Nothing here reaches behind the public surface: the control API, the
# observability reads, and the peer's own gRPC server are the whole instrument.

set -u
cd "$(dirname "$0")"
ROOT=$(cd ../../.. && pwd)
: "${RIMSKY_IMAGE_TAG:?export RIMSKY_IMAGE_TAG=src-<tree hash> first}"
NET=exp-execproto-net
STACK=exp-execproto-stack
PEER=exp-execproto-peer
PORT=${PORT:-19311}
WORK=$(mktemp -d)
BASE="http://127.0.0.1:$PORT"

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
    -H "Idempotency-Key: ep-$RANDOM$RANDOM" ${b:+-d "$b"} "$BASE$p"; }
code() { req "$@" | tail -1; }
body() { req "$@" | sed '$d'; }

note "== build the third-party executor =="
cp -r peer "$WORK/peer"
sed "s|RIMSKY_PROTOCOLS_PATH|$ROOT/lib/protocols|" "$WORK/peer/go.mod.tmpl" > "$WORK/peer/go.mod"
rm "$WORK/peer/go.mod.tmpl"
ARCH=$(docker info --format '{{.Architecture}}' | sed 's/aarch64/arm64/;s/x86_64/amd64/')
( cd "$WORK/peer" && GOOS=linux GOARCH="$ARCH" CGO_ENABLED=0 GOFLAGS=-mod=mod GOPROXY=off GOSUMDB=off go build -o "$WORK/peer-linux" . ) \
  && check "the peer builds for the stack's platform" yes yes || { check "the peer builds for the stack's platform" yes no; note "EXPERIMENT FAIL"; exit 1; }

note
note "== bring up a stack that knows only the peer's endpoint =="
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
docker run -d --name "$STACK" --network "$NET" -p "$PORT:8080" \
  -v "$WORK/rimsky.yml:/etc/rimsky/rimsky.yml:ro" "rimsky-all-in-one:$RIMSKY_IMAGE_TAG" >/dev/null || exit 1
until [ "$(code GET /v1/health)" = 200 ]; do sleep 0.5; done

note "waiting for the stack's discovery probe to reach the peer (blocks until it does)"
until [ "$(body GET /v1/observability/executors/third-party | jq -r '.peer.reachability_status')" = reachable ]; do sleep 0.5; done
CAPS=$(body GET /v1/observability/executors/third-party)
check "rimsky discovered the peer at startup" reachable "$(printf '%s' "$CAPS" | jq -r .peer.reachability_status)"
check "the peer's declared error classes reached rimsky" "third-party/broken,third-party/refused" \
  "$(printf '%s' "$CAPS" | jq -r '.peer.observability_capabilities.declared_error_classes|sort|join(",")')"
check "the peer's declared tags reached rimsky" "third-party.refused,third-party.served" \
  "$(printf '%s' "$CAPS" | jq -r '.peer.observability_capabilities.declared_tags|sort|join(",")')"
check "the peer's expected-attributes schema reached rimsky" "additionalProperties" \
  "$(printf '%s' "$CAPS" | jq -r '.peer.observability_capabilities.expected_attributes_schema' | base64 -d | jq -r 'keys|join(",")' | grep -o additionalProperties)"

note
note "== rimsky validates template attributes against the peer's advertised schema =="
verrs() { body POST /v1/templates/validate "$1" | jq -r '[.validation_errors[]?.msg]|join(" ~ ")'; }
vwarns() { body POST /v1/templates/validate "$1" | jq -r '[.validation_warnings[]?.msg]|join(" ~ ")'; }
contains "a property whose type contradicts the peer is rejected" \
  "the executor is authoritative on types" \
  "$(verrs '{"spec":{"name":"bad","version":"1","nodes":[{"type":"w","executor":"third-party","attributes":{"schema":{"type":"object","properties":{"echo":{"type":"integer"}}}}}]}}')"
contains "a property the peer's closed schema does not declare is rejected" \
  "is not declared in executor's expected_attributes_schema" \
  "$(verrs '{"spec":{"name":"bad","version":"1","nodes":[{"type":"w","executor":"third-party","attributes":{"schema":{"type":"object","properties":{"bogus":{"type":"string"}}}}}]}}')"
contains "an error class outside the peer's declared vocabulary is flagged" \
  "is not in any declared vocabulary" \
  "$(vwarns '{"spec":{"name":"warn","version":"1","nodes":[{"type":"w","executor":"third-party","error_types":{"nope/unknown":{"action":"give_up"}},"attributes":{"schema":{"type":"object","properties":{"echo":{"type":"string"}}}}}]}}')"
contains "a subscription filtering on a tag the peer never declared is rejected" \
  "is not declared by sender" \
  "$(verrs '{"spec":{"name":"bad","version":"1","nodes":[{"type":"w","executor":"third-party","attributes":{"schema":{"type":"object","properties":{"echo":{"type":"string"}}}}},{"type":"d","executor":"third-party","subscribes":[{"node":"w","type":"terminal/success","when":"\"third-party.nope\" in payload.tags","force_upstream_refresh":false}],"attributes":{"schema":{"type":"object","properties":{"echo":{"type":"string"}}}}}]}}')"

note
note "== a template written to the peer's advertisement registers and deploys =="
SPEC='{"tag":"execproto","spec":{"name":"execproto","version":"1","nodes":[
 {"type":"okworker","executor":"third-party","attributes":{"schema":{"type":"object","properties":{"outcome":{"type":"string","default":"ok"},"echo":{"type":"string","default":"hi"}}}}},
 {"type":"served_downstream","executor":"third-party","subscribes":[{"node":"okworker","type":"terminal/success","when":"\"third-party.served\" in payload.tags","force_upstream_refresh":false}],"attributes":{"schema":{"type":"object","properties":{"outcome":{"type":"string","default":"ok"},"echo":{"type":"string","default":"ds"}}}}},
 {"type":"refused_downstream","executor":"third-party","subscribes":[{"node":"okworker","type":"terminal/success","when":"\"third-party.refused\" in payload.tags","force_upstream_refresh":false}],"attributes":{"schema":{"type":"object","properties":{"outcome":{"type":"string","default":"ok"},"echo":{"type":"string","default":"rd"}}}}},
 {"type":"failfast","executor":"third-party","error_types":{"third-party/refused":{"action":"give_up"}},"attributes":{"schema":{"type":"object","properties":{"outcome":{"type":"string","default":"fail"},"echo":{"type":"string","default":"ff"}}}}},
 {"type":"retrier","max_retries":2,"executor":"third-party","error_types":{"third-party/broken":{"action":"retry"}},"attributes":{"schema":{"type":"object","properties":{"outcome":{"type":"string","default":"broken"},"echo":{"type":"string","default":"rt"}}}}},
 {"type":"parker","executor":"third-party","attributes":{"schema":{"type":"object","properties":{"outcome":{"type":"string","default":"park"},"echo":{"type":"string","default":"pk"}}}}}
]}}'
REG=$(body POST /v1/templates "$SPEC")
TPL=$(printf '%s' "$REG" | jq -r '.template_id // empty')
check "template registered" yes "$([ -n "$TPL" ] && echo yes || echo no)"
[ -n "$TPL" ] || { note "register response: $REG"; note "EXPERIMENT FAIL"; exit 1; }
check "template deployed" 200 "$(code POST "/v1/templates/$TPL/deploy" '{}')"

IID=$(body POST /v1/instances "{\"template\":\"$TPL\",\"instance_key\":\"execproto-1\",\"params\":{},\"target_agent\":\"execproto-agent\"}" | jq -r '.instance_id // empty')
check "instance created" yes "$([ -n "$IID" ] && echo yes || echo no)"
[ -n "$IID" ] || { note "EXPERIMENT FAIL"; exit 1; }
code POST "/v1/instances/$IID/messages" '{"type":""}' >/dev/null

summary() { body GET "/v1/observability/nodes/$IID/$1" | jq -c '.run_summary'; }
settle() { # settle <node type> -> fresh|failed|parked  (blocks until the node settles)
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

note
note "== the peer's settling terminal outcomes (blocks until each node settles) =="
check "a success outcome settles the node fresh" fresh "$(settle okworker)"
check "the peer's attribute delta landed on the node" third-party-peer \
  "$(body GET "/v1/observability/nodes/$IID/okworker" | jq -r '.latest_attributes.served_by // empty')"
check "an error outcome settles the node failed" failed "$(settle failfast)"
check "a retried error outcome settles the node failed" failed "$(settle retrier)"

note "waiting for the park outcome to leave a parked node (blocks until it does)"
until [ "$(body GET /v1/admin/diagnostics/parked-nodes | jq -r --arg i "$IID" '[.parked_nodes[]?|select(.instance_id==$i)]|length')" -gt 0 ]; do sleep 0.5; done
PARKED_ID=$(body GET /v1/admin/diagnostics/parked-nodes | jq -r --arg i "$IID" '[.parked_nodes[]?|select(.instance_id==$i)|.node_id]|first')
check "a park outcome parks the node rather than settling it" parker \
  "$(body GET "/v1/nodes/$PARKED_ID" | jq -r '.node_type // empty')"
check "the parked node carries the peer's park signal" transient/park \
  "$(body GET "/v1/nodes/$PARKED_ID" | jq -r '.settling_signal_type // empty')"

note
note "== rimsky routed the peer's errors by the class the peer declared =="
DISPATCHES=$(docker logs "$PEER" 2>&1 | grep -c 'node=failfast')
check "the give_up class was dispatched once" 1 "$DISPATCHES"
RETRIES=$(docker logs "$PEER" 2>&1 | grep -c 'node=retrier')
check "the retry class was dispatched once plus its two retries" 3 "$RETRIES"

note
note "== the peer's declared tags gate a tag-filtered subscription =="
check "the subscriber filtering on the emitted tag ran" fresh "$(settle served_downstream)"
check "the subscriber filtering on the other declared tag never ran" '{"active_count":0,"pending_count":0,"fresh_count":0,"failed_count":0}' \
  "$(summary refused_downstream)"

note
note "peer container log:"
docker logs "$PEER" 2>&1 | tail -8 | sed 's/^/    /'

note
if [ "$fail" = 0 ]; then note "EXPERIMENT PASS"; else note "EXPERIMENT FAIL"; fi
exit "$fail"
