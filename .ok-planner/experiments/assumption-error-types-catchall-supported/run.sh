#!/usr/bin/env bash
# Experiment: assumption-error-types-catchall-supported
#
# A template author wants one fallback policy instead of enumerating error
# classes, and writes `error_types: {"*": {action: ...}}`. This run registers
# four versions of one node -- keyed on the exact class, on the emitter's
# prefix family, on a bare `*`, and with no error_types at all -- then provokes
# the same error in each and compares how the node settles.
set -uo pipefail

: "${RIMSKY_IMAGE_TAG:?export RIMSKY_IMAGE_TAG=src-<tree hash> first}"
TAG="$RIMSKY_IMAGE_TAG"
WORK="$(mktemp -d)"

NET=exp-assumption-catchall-net
STACK=exp-assumption-catchall-stack
EXECUTOR=exp-assumption-catchall-executor

fails=0
ok()  { printf 'PASS  %s\n' "$*"; }
bad() { printf 'FAIL  %s\n' "$*"; fails=$((fails+1)); }
check() { if [ "$2" = "$3" ]; then ok "$1 [$3]"; else bad "$1 expected [$2] got [$3]"; fi; }
has() { case "$2" in *"$1"*) ok "$3";; *) bad "$3 (missing '$1')";; esac; }

free_port() { python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()'; }
uid() { python3 -c 'import uuid;print(uuid.uuid4().hex)'; }

cleanup() {
  docker rm -f "$STACK" "$EXECUTOR" >/dev/null 2>&1
  docker network rm "$NET" >/dev/null 2>&1
  rm -rf "$WORK"
}
trap cleanup EXIT

cat > "$WORK/rimsky.yml" <<'YAML'
persistence:
  driver: sqlite
  sqlite:
    path: /var/lib/rimsky/state.db
named_locks: {}
claim_producers: {}
executors:
  "http-node":
    transport: grpc
    endpoint: "executor:9091"
    protocols: ["executor"]
    observability_endpoint: "executor:9091"
YAML

PORT=$(free_port); BASE="http://127.0.0.1:$PORT"
docker rm -f "$STACK" "$EXECUTOR" >/dev/null 2>&1
docker network rm "$NET" >/dev/null 2>&1
docker network create "$NET" >/dev/null || exit 1
docker run -d --name "$EXECUTOR" --network "$NET" --network-alias executor "rimsky-executor-http-node:$TAG" >/dev/null || exit 1
docker run -d --name "$STACK" --network "$NET" --network-alias rimsky \
  -e RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_HOST=rimsky -p "127.0.0.1:$PORT:8080" \
  -v "$WORK/rimsky.yml:/etc/rimsky/rimsky.yml:ro" "rimsky-all-in-one:$TAG" >/dev/null || exit 1

get()  { curl -s "$BASE$1"; }
post() { curl -s -X POST -H 'content-type: application/json' -H "Idempotency-Key: $(uid)" -d "$2" "$BASE$1"; }
until [ "$(curl -s -o /dev/null -w '%{http_code}' "$BASE/v1/health")" = 200 ]; do sleep 0.5; done

spec() { cat <<JSON
{"tag":"$1","spec":{"name":"$1","version":"1","nodes":[{"type":"w","executor":"http-node","error_types":$2,"attributes":{"schema":{"type":"object","properties":{"url":{"type":"string","default":"http://nowhere.invalid/x"}}}}}]}}
JSON
}

register() { post /v1/templates "$1"; }

settle() {
  tpl=$(printf '%s' "$1" | jq -r '.template_id // empty')
  post "/v1/templates/$tpl/deploy" '{}' >/dev/null
  iid=$(post /v1/instances "$(printf '{"template":"%s","instance_key":"%s","target_agent":"audit-agent","params":{}}' "$tpl" "$2")" | jq -r '.instance_id // empty')
  post "/v1/instances/$iid/messages" '{}' >/dev/null
  while :; do
    s=$(get "/v1/observability/nodes/$iid/w" | jq -r '.run_summary as $r | if $r == null then "in-flight" elif $r.failed_count>0 then "failed" elif $r.fresh_count>0 and $r.active_count==0 and $r.pending_count==0 then "fresh" else "in-flight" end')
    [ "$s" = in-flight ] || break
    sleep 0.4
  done
  printf '%s' "$s"
}

echo "--- the exact class routes, and is the baseline"
R=$(register "$(spec c-exact '{"http/network_error":{"action":"pass"}}')")
check "the exact-class policy registers with no warning" 0 "$(printf '%s' "$R" | jq '[.validation_warnings[]?]|length')"
check "the node passes the error the policy names" fresh "$(settle "$R" k-exact)"

echo "--- a bare * does not catch it"
R=$(register "$(spec c-star '{"*":{"action":"pass"}}')")
check "the catch-all registers" yes "$(printf '%s' "$R" | jq -r 'if .template_id then "yes" else "no" end')"
W=$(printf '%s' "$R" | jq -r '[.validation_warnings[]?.msg]|join(" ")')
printf '    %s\n' "$W"
has 'error class "*" is not in any declared vocabulary' "$W" "the validator warns that * is in no vocabulary"
has 'will only match if a peer emits this exact class' "$W" "the warning says matching is by exact class"
check "the node fails despite the catch-all" failed "$(settle "$R" k-star)"

echo "--- an emitter-prefix family does not catch it either"
R=$(register "$(spec c-prefix '{"http/*":{"action":"pass"}}')")
check "the node fails despite http/*" failed "$(settle "$R" k-prefix)"

echo "--- which is exactly what declaring nothing does"
R=$(register "$(spec c-none '{}')")
check "a node with no error_types fails the same way" failed "$(settle "$R" k-none)"

echo
if [ "$fails" -eq 0 ]; then echo "EXPERIMENT PASS"; else echo "EXPERIMENT FAIL ($fails)"; fi
exit "$fails"
