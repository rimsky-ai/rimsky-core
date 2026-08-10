#!/usr/bin/env bash
# Experiment: audit-artifact
#
# The two one-shot modes both leave a durable record behind, and an operator can
# inspect a completed run's record afterwards without re-running it.
#
#   Way A — `rimsky compose run` drives a mixed manifest (one instance succeeds,
#           one fails) to terminal in the invocation that started it.
#   Way B — `rimsky run` self-hosts an ad-hoc template with a success leg and a
#           failure leg.
#
# Each leaves a per-run artifact directory. The record inside it is then read
# back through the product's own read surface: a copy of the artifact's state is
# served by a `rimsky-all-in-one` container and queried with `rimsky instance
# list|get|nodes|events`, `rimsky node get` and the observability routes. Nothing
# is re-run: the executor binary is gone before any of this happens.

set -u
cd "$(dirname "$0")"
ROOT=$(cd ../../.. && pwd)
: "${RIMSKY_IMAGE_TAG:?export RIMSKY_IMAGE_TAG=src-<tree hash> first}"
CLI="$ROOT/bin/rimsky"
PEERSRC="$ROOT/.ok-planner/experiments/permissive-peer-build/peer"
SERVE=exp-aa-serve
PORT=${PORT:-18521}
WORK=$(mktemp -d)
BASE="http://127.0.0.1:$PORT"
# Keep the Go build caches pointed at the real home, then move HOME so the CLI
# reads no pre-existing context (a configured endpoint would defeat self-hosting).
export GOPATH="$(go env GOPATH)" GOMODCACHE="$(go env GOMODCACHE)" GOCACHE="$(go env GOCACHE)"
export HOME="$WORK"
unset RIMSKY_API_KEY RIMSKY_CONTROL_API_URL 2>/dev/null

fail=0
note() { printf '%s\n' "$*"; }
check() {
  if [ "$2" = "$3" ]; then printf 'PASS  %-62s %s\n' "$1" "$3"
  else printf 'FAIL  %-62s expected [%s] got [%s]\n' "$1" "$2" "$3"; fail=1; fi
}
cleanup() { docker rm -f "$SERVE" >/dev/null 2>&1; rm -rf "$WORK"; }
trap cleanup EXIT

get() { curl -sS -m 15 "$BASE$1"; }

# Serve a copy of a completed run's record through a rimsky stack, so the record
# can be read with the same verbs and routes any deployed stack answers.
serve_record() { # serve_record <run dir>
  docker rm -f "$SERVE" >/dev/null 2>&1
  rm -rf "$WORK/serve"; mkdir -p "$WORK/serve"
  cp "$1/state.db" "$WORK/serve/state.db"
  docker run -d --name "$SERVE" -p "$PORT:8080" -v "$WORK/serve:/var/lib/rimsky" \
    "rimsky-all-in-one:$RIMSKY_IMAGE_TAG" >/dev/null || exit 1
  until [ "$(curl -sS -m 3 -o /dev/null -w '%{http_code}' "$BASE/v1/health" 2>/dev/null)" = 200 ]; do sleep 0.5; done
}
run_dir() { # run_dir <workdir holding .rimsky>
  local d; d=$(cd "$1" && readlink .rimsky/latest)
  case "$d" in /*) printf '%s' "$d" ;; *) printf '%s' "$1/.rimsky/$d" ;; esac
}
kinds_for() { # kinds_for <instance id>
  get "/v1/events?instance_id=$1&limit=200" | jq -r '[.events[].kind]|sort|unique|join(" ")'
}

note "== build the executor the one-shots dispatch to =="
cp -r "$PEERSRC" "$WORK/peer"
sed "s|RIMSKY_PROTOCOLS_PATH|$ROOT/lib/protocols|" "$WORK/peer/go.mod.tmpl" > "$WORK/peer/go.mod"
rm "$WORK/peer/go.mod.tmpl"
( cd "$WORK/peer" && GOFLAGS=-mod=mod GOPROXY=off GOSUMDB=off go build -o "$WORK/peer-host" . ) || exit 1
check "peer executor built" yes yes

note
note "== way A: the compose one-shot =="
cp -r manifest "$WORK/manifest"
( cd "$WORK/manifest" && "$CLI" compose run --service "peer=$WORK/peer-host" ./rimsky-compose.yml ) \
  > "$WORK/composerun.out" 2> "$WORK/composerun.err"
RC=$?
check "the invocation that started the run finished it" 1 "$RC"
check "it reported the succeeding instance" yes \
  "$(grep -q 'audit-artifact/ok: success' "$WORK/composerun.err" && echo yes || echo no)"
check "it reported the failing instance" yes \
  "$(grep -q 'audit-artifact/oops: failure' "$WORK/composerun.err" && echo yes || echo no)"
check "the executor process the run spawned is gone" yes \
  "$(pgrep -f "$WORK/peer-host" >/dev/null 2>&1 && echo no || echo yes)"

ARUN=$(run_dir "$WORK/manifest")
check "the run left an artifact directory" yes "$([ -d "$ARUN" ] && echo yes || echo no)"
check "the artifact carries the run's state" yes "$([ -f "$ARUN/state.db" ] && echo yes || echo no)"
check "the artifact carries the run's blob store" yes "$([ -d "$ARUN/blobs" ] && echo yes || echo no)"
check "the artifact carries the config the run used" yes "$([ -f "$ARUN/rimsky.yml" ] && echo yes || echo no)"

note
note "== way A: inspecting the record afterwards, through the product's read surface =="
serve_record "$ARUN"
INSTANCES=$(get /v1/instances)
check "both instances are in the record, both terminal" "2 2" \
  "$(printf '%s' "$INSTANCES" | jq -r '"\(.instances|length) \([.instances[]|select(.terminated_at != null)]|length)"')"
IDS=$(printf '%s' "$INSTANCES" | jq -r '.instances[].id')
KINDS_ALL=""
for id in $IDS; do KINDS_ALL="$KINDS_ALL $(kinds_for "$id")"; done
check "the record replays a success terminal" yes \
  "$(printf '%s' "$KINDS_ALL" | grep -q 'terminal/success' && echo yes || echo no)"
check "the record replays the failure with its error class" yes \
  "$(printf '%s' "$KINDS_ALL" | grep -q 'terminal/error/third-party/refused' && echo yes || echo no)"
FAILID=$(for id in $IDS; do kinds_for "$id" | grep -q 'terminal/error' && echo "$id"; done)
OKID=$(for id in $IDS; do kinds_for "$id" | grep -q 'terminal/error' || echo "$id"; done)
check "instance get reads the failing run's instance" yes \
  "$([ "$("$CLI" instance get "$FAILID" --endpoint "$BASE" -o json | jq -r '.instance.id // .id')" = "$FAILID" ] && echo yes || echo no)"
check "instance nodes reads the failing run's worker" worker \
  "$("$CLI" instance nodes "$FAILID" --endpoint "$BASE" -o json | jq -r '[.[].node_type]|map(select(.!=""))|unique|join(" ")')"
check "instance events replays the failing run's terminal" yes \
  "$("$CLI" instance events "$FAILID" --endpoint "$BASE" -o json | jq -sr 'if ([.[].kind]|map(select(startswith("terminal/error")))|length) > 0 then "yes" else "no" end')"
NODEID=$("$CLI" instance nodes "$OKID" --endpoint "$BASE" -o json | jq -r '[.[]|select(.node_type=="worker").id]|first')
check "node get reads one node of the succeeding run" worker \
  "$("$CLI" node get "$NODEID" --endpoint "$BASE" -o json | jq -r '.node_type // .node.node_type // empty')"
check "the succeeding run's attribute writeback is readable" third-party-peer \
  "$(get "/v1/observability/nodes/$OKID/worker" | jq -r '.latest_attributes.served_by // empty')"
BEFORE=$(get "/v1/events?instance_id=$FAILID&limit=200" | jq '.events|length')
AFTER=$(get "/v1/events?instance_id=$FAILID&limit=200" | jq '.events|length')
check "reading the record does not change it" "$BEFORE" "$AFTER"

note
note "== way B: the ad-hoc one-shot =="
mkdir -p "$WORK/adhoc"
cat > "$WORK/adhoc/template.yml" <<'YAML'
name: audit-artifact-adhoc
version: "1"
message_queue_mode: backlog
nodes:
  - type: okworker
    executor: peer
    attributes:
      schema:
        type: object
        properties:
          outcome: {type: string, default: ok}
          echo: {type: string, default: adhoc-success-leg}
  - type: failworker
    executor: peer
    attributes:
      schema:
        type: object
        properties:
          outcome: {type: string, default: fail}
          echo: {type: string, default: adhoc-failure-leg}
YAML
( cd "$WORK/adhoc" && "$CLI" run --service "peer=$WORK/peer-host" ./template.yml ) \
  > "$WORK/adhoc.out" 2> "$WORK/adhoc.err"
BRC=$?
check "the ad-hoc one-shot finished in the invocation that started it" 1 "$BRC"
BRUN=$(run_dir "$WORK/adhoc")
check "it left an artifact directory too" yes "$([ -d "$BRUN" ] && [ -f "$BRUN/state.db" ] && echo yes || echo no)"
serve_record "$BRUN"
BID=$(get /v1/instances | jq -r '.instances[0].id')
check "its instance is in the record, terminal" yes \
  "$(get /v1/instances | jq -r 'if (.instances[0].terminated_at != null) then "yes" else "no" end')"
BKINDS=$(kinds_for "$BID")
check "the record replays both legs of the ad-hoc run" yes \
  "$(printf '%s' "$BKINDS" | grep -q 'terminal/success' && printf '%s' "$BKINDS" | grep -q 'terminal/error/third-party/refused' && echo yes || echo no)"
check "the ad-hoc success leg's writeback is readable" third-party-peer \
  "$(get "/v1/observability/nodes/$BID/okworker" | jq -r '.latest_attributes.served_by // empty')"

note
if [ "$fail" = 0 ]; then note "EXPERIMENT PASS"; else note "EXPERIMENT FAIL"; fi
exit "$fail"
