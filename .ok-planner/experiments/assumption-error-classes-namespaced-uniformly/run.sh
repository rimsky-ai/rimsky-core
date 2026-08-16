#!/usr/bin/env bash
# Experiment: assumption-error-classes-namespaced-uniformly
#
# A template author writing error routing wants to key `error_types` on
# emitter families -- `http/*`, `agent/*`, `verifier/*`. This run asks the
# product two questions: which classes carry an emitter prefix at all, and
# whether a `<prefix>/*` key routes anything. It registers one node per
# candidate class and reads the validator's vocabulary warnings, then provokes
# a real `http/network_error` against nodes keyed the exact way and the family
# way.
set -uo pipefail

: "${RIMSKY_IMAGE_TAG:?export RIMSKY_IMAGE_TAG=src-<tree hash> first}"
TAG="$RIMSKY_IMAGE_TAG"
WORK="$(mktemp -d)"

NET=exp-assumption-namespaced-net
STACK=exp-assumption-namespaced-stack
EXECUTOR=exp-assumption-namespaced-executor
CLAUDE=exp-assumption-namespaced-claude

fails=0
ok()  { printf 'PASS  %s\n' "$*"; }
bad() { printf 'FAIL  %s\n' "$*"; fails=$((fails+1)); }
check() { if [ "$2" = "$3" ]; then ok "$1 [$3]"; else bad "$1 expected [$2] got [$3]"; fi; }

free_port() { python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()'; }
uid() { python3 -c 'import uuid;print(uuid.uuid4().hex)'; }

cleanup() {
  docker rm -f "$STACK" "$EXECUTOR" "$CLAUDE" >/dev/null 2>&1
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
  "claude-agent":
    transport: grpc
    endpoint: "claude:9090"
    protocols: ["executor"]
    observability_endpoint: "claude:9090"
YAML

PORT=$(free_port); BASE="http://127.0.0.1:$PORT"
docker rm -f "$STACK" "$EXECUTOR" "$CLAUDE" >/dev/null 2>&1
docker network rm "$NET" >/dev/null 2>&1
docker network create "$NET" >/dev/null || exit 1
docker run -d --name "$EXECUTOR" --network "$NET" --network-alias executor "rimsky-executor-http-node:$TAG" >/dev/null || exit 1
docker run -d --name "$CLAUDE" --network "$NET" --network-alias claude -e RIMSKY_EXECUTOR_STUB_MODE=1 "rimsky-executor-claude-agent:$TAG" >/dev/null || exit 1
docker run -d --name "$STACK" --network "$NET" --network-alias rimsky \
  -e RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_HOST=rimsky -p "127.0.0.1:$PORT:8080" \
  -v "$WORK/rimsky.yml:/etc/rimsky/rimsky.yml:ro" "rimsky-all-in-one:$TAG" >/dev/null || exit 1

get()  { curl -s "$BASE$1"; }
post() { curl -s -X POST -H 'content-type: application/json' -H "Idempotency-Key: $(uid)" -d "$2" "$BASE$1"; }
until [ "$(curl -s -o /dev/null -w '%{http_code}' "$BASE/v1/health")" = 200 ]; do sleep 0.5; done

http_spec() { cat <<JSON
{"tag":"$1","spec":{"name":"$1","version":"1","nodes":[{"type":"w","executor":"http-node","error_types":{"$2":{"action":"pass"}},"attributes":{"schema":{"type":"object","properties":{"url":{"type":"string","default":"http://nowhere.invalid/x"}}}}}]}}
JSON
}
claude_spec() { cat <<JSON
{"tag":"$1","spec":{"name":"$1","version":"1","nodes":[{"type":"w","executor":"claude-agent","error_types":{"$2":{"action":"pass"}},"attributes":{"schema":{"type":"object","properties":{"system_prompt":{"type":"string","default":"s"},"user_prompt":{"type":"string","default":"h"},"cli":{"type":"object","default":{}}}}}}]}}
JSON
}
warnings_for() { post /v1/templates "$1" | jq -r '[.validation_warnings[]?]|length'; }

echo "--- the classes rimsky itself raises carry no emitter prefix, and the validator knows them"
for c in template_resolution_failed template_validation_failed executor_schema_unavailable \
         attributes_schema_failed unresolved_executor executor_sync_timeout \
         executor_protocol_violation abandoned; do
  n=$(warnings_for "$(http_spec "n-$c" "$c")")
  check "error_types keyed on the prefixless class $c registers with no vocabulary warning" 0 "$n"
done

echo "--- the one prefixless class in the published catalog is in no vocabulary at all"
check "spawn_failed warns on an http-node node" 1 "$(warnings_for "$(http_spec n-spawn-http spawn_failed)")"
check "spawn_failed warns on a claude-agent node too" 1 "$(warnings_for "$(claude_spec n-spawn-claude spawn_failed)")"

echo "--- a class is only in the vocabulary of the executor that declared it"
check "agent/refused warns on an http-node node" 1 "$(warnings_for "$(http_spec n-agent-on-http agent/refused)")"
check "agent/refused is clean on a claude-agent node" 0 "$(warnings_for "$(claude_spec n-agent-on-claude agent/refused)")"

echo "--- <prefix>/* is not a key the validator recognises, except for acquire/"
check "http/* warns" 1 "$(warnings_for "$(http_spec n-httpstar 'http/*')")"
check "acquire/* is clean" 0 "$(warnings_for "$(http_spec n-acqstar 'acquire/*')")"

settle() {
  tpl=$(post /v1/templates "$1" | jq -r '.template_id // empty')
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

echo "--- and <prefix>/* routes nothing at dispatch"
check "the exact class http/network_error routes the failure" fresh "$(settle "$(http_spec r-exact http/network_error)" k-r-exact)"
check "http/* leaves the same failure unrouted" failed "$(settle "$(http_spec r-family 'http/*')" k-r-family)"
check "http/request_invalid/* — a family the executor itself declares — routes nothing either" failed \
  "$(settle "$(http_spec r-declared-family 'http/request_invalid/*')" k-r-declared)"

echo
if [ "$fails" -eq 0 ]; then echo "EXPERIMENT PASS"; else echo "EXPERIMENT FAIL ($fails)"; fi
exit "$fails"
