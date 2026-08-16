#!/usr/bin/env bash
# Experiment: assumption-error-classes-stable-across-releases
#
# An operator hard-codes error classes in templates and wants to know what kind
# of thing the class catalog is. This run reads the class list every bundled
# executor advertises in its capabilities handshake, looks for any version or
# deprecation marker on that handshake, and then asks a live stack which class
# names it recognises -- per executor, and for the classes rimsky raises itself.
set -uo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../../.." && pwd)"
: "${RIMSKY_IMAGE_TAG:?export RIMSKY_IMAGE_TAG=src-<tree hash> first}"
TAG="$RIMSKY_IMAGE_TAG"
WORK="$(mktemp -d)"

NET=exp-assumption-classcat-net
STACK=exp-assumption-classcat-stack
HTTPNODE=exp-assumption-classcat-httpnode
VHTTP=exp-assumption-classcat-vhttp
VSHAPES=exp-assumption-classcat-vshapes
CLAUDE=exp-assumption-classcat-claude

fails=0
ok()  { printf 'PASS  %s\n' "$*"; }
bad() { printf 'FAIL  %s\n' "$*"; fails=$((fails+1)); }
check() { if [ "$2" = "$3" ]; then ok "$1 [$3]"; else bad "$1 expected [$2] got [$3]"; fi; }

free_port() { python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()'; }
uid() { python3 -c 'import uuid;print(uuid.uuid4().hex)'; }

cleanup() {
  docker rm -f "$STACK" "$HTTPNODE" "$VHTTP" "$VSHAPES" "$CLAUDE" >/dev/null 2>&1
  docker network rm "$NET" >/dev/null 2>&1
  rm -rf "$WORK"
}
trap cleanup EXIT

cp -r "$HERE/probe" "$WORK/probe"
sed "s|RIMSKY_PROTOCOLS_PATH|$ROOT/lib/protocols|" "$WORK/probe/go.mod.tmpl" > "$WORK/probe/go.mod"
rm "$WORK/probe/go.mod.tmpl"
(cd "$WORK/probe" && GOFLAGS=-mod=mod go build -o "$WORK/caps" .) || { echo "probe build failed"; exit 1; }

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
    endpoint: "httpnode:9091"
    protocols: ["executor"]
    observability_endpoint: "httpnode:9091"
  "claude-agent":
    transport: grpc
    endpoint: "claude:9090"
    protocols: ["executor"]
    observability_endpoint: "claude:9090"
YAML

PORT=$(free_port); BASE="http://127.0.0.1:$PORT"
P_HTTP=$(free_port); P_VHTTP=$(free_port); P_VSHAPES=$(free_port); P_CLAUDE=$(free_port)
docker rm -f "$STACK" "$HTTPNODE" "$VHTTP" "$VSHAPES" "$CLAUDE" >/dev/null 2>&1
docker network rm "$NET" >/dev/null 2>&1
docker network create "$NET" >/dev/null || exit 1
docker run -d --name "$HTTPNODE" --network "$NET" --network-alias httpnode -p "127.0.0.1:$P_HTTP:9091" "rimsky-executor-http-node:$TAG" >/dev/null || exit 1
docker run -d --name "$VHTTP" --network "$NET" --network-alias vhttp -e RIMSKY_EXECUTOR_PORT_GRPC=9096 -p "127.0.0.1:$P_VHTTP:9096" "rimsky-executor-verifier-http:$TAG" >/dev/null || exit 1
docker run -d --name "$VSHAPES" --network "$NET" --network-alias vshapes -e RIMSKY_EXECUTOR_PORT_GRPC=9095 -p "127.0.0.1:$P_VSHAPES:9095" "rimsky-executor-verifier-shape-checks:$TAG" >/dev/null || exit 1
docker run -d --name "$CLAUDE" --network "$NET" --network-alias claude -e RIMSKY_EXECUTOR_STUB_MODE=1 -p "127.0.0.1:$P_CLAUDE:9090" "rimsky-executor-claude-agent:$TAG" >/dev/null || exit 1
docker run -d --name "$STACK" --network "$NET" --network-alias rimsky \
  -e RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_HOST=rimsky -p "127.0.0.1:$PORT:8080" \
  -v "$WORK/rimsky.yml:/etc/rimsky/rimsky.yml:ro" "rimsky-all-in-one:$TAG" >/dev/null || exit 1
for p in "$P_HTTP" "$P_VHTTP" "$P_VSHAPES" "$P_CLAUDE"; do until nc -z 127.0.0.1 "$p" >/dev/null 2>&1; do sleep 0.2; done; done

echo "--- the catalog is whatever the running executors advertise"
: > "$WORK/all.txt"
for pair in "http-node:$P_HTTP" "verifier-http:$P_VHTTP" "verifier-shape-checks:$P_VSHAPES" "claude-agent:$P_CLAUDE"; do
  n=${pair%%:*}; p=${pair##*:}
  "$WORK/caps" -addr "127.0.0.1:$p" > "$WORK/$n.json" || exit 1
  jq -r '.declaredErrorClasses[]?' "$WORK/$n.json" >> "$WORK/all.txt"
  printf '    %-24s %s classes\n' "$n" "$(jq -r '[.declaredErrorClasses[]?]|length' "$WORK/$n.json")"
done
sort -u "$WORK/all.txt" > "$WORK/union.txt"
check "the four bundled executors advertise 27 distinct classes between them" 27 "$(wc -l < "$WORK/union.txt" | tr -d ' ')"
check "no executor advertises spawn_failed" 0 "$(grep -c '^spawn_failed$' "$WORK/union.txt" || true)"

echo "--- nothing on the handshake versions that list"
KEYS=$(jq -r 'keys|join(",")' "$WORK/http-node.json")
printf '    capabilities keys: %s\n' "$KEYS"
case "$KEYS" in
  *version*|*deprecat*|*schema_version*) bad "the capabilities handshake carries a version marker after all";;
  *) ok "the capabilities handshake carries no version or deprecation marker";;
esac

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

echo "--- a class is valid or not depending on which executor the node names"
check "agent/refused is unknown on an http-node node" 1 "$(warnings_for "$(http_spec s-agent-http agent/refused)")"
check "agent/refused is known on a claude-agent node" 0 "$(warnings_for "$(claude_spec s-agent-claude agent/refused)")"
check "http/timeout is unknown on a claude-agent node" 1 "$(warnings_for "$(claude_spec s-http-claude http/timeout)")"
check "http/timeout is known on an http-node node" 0 "$(warnings_for "$(http_spec s-http-http http/timeout)")"

echo "--- and rimsky raises classes no executor advertises"
for c in template_resolution_failed template_validation_failed executor_schema_unavailable \
         attributes_schema_failed unresolved_executor executor_sync_timeout \
         executor_protocol_violation abandoned; do
  check "$c is accepted by the validator but advertised by no executor" "0,0" \
    "$(warnings_for "$(http_spec s-$c "$c")"),$(grep -c "^$c$" "$WORK/union.txt" || true)"
done
check "the acquire/ family is recognised too and is advertised by no executor" "0,0" \
  "$(warnings_for "$(http_spec s-acquire 'acquire/unavailable')"),$(grep -c '^acquire/' "$WORK/union.txt" || true)"

echo
if [ "$fails" -eq 0 ]; then echo "EXPERIMENT PASS"; else echo "EXPERIMENT FAIL ($fails)"; fi
exit "$fails"
