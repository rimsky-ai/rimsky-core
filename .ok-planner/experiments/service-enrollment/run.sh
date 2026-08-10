#!/usr/bin/env bash
# Experiment: service-enrollment
#
# A standing service under mutual-TLS peer auth is given one api-key and nothing
# else. The run checks each half of what that buys the operator:
#   mintable   -> the key is minted from the ordinary key surface
#   scopeable  -> its grant carries the enrollment action alone, and it can do
#                 nothing else against the control API
#   at startup -> the service, holding only that key, comes up serving a
#                 deployment-signed certificate and refusing unauthenticated
#                 clients, and the stack dispatches work through it
#   renewed    -> the deployment re-issues to that same key with no operator
#                 action, each issuance a distinct short-lived credential; the
#                 service re-acquires on its own whenever it needs to
#   revocable  -> revoking the one key stops future issuance: the enrollment
#                 route refuses it, and a service restarted on it fails closed
#
# The service is the third-party peer built for permissive-peer-build; it takes
# its whole peer-auth posture from the environment.

set -u
cd "$(dirname "$0")"
ROOT=$(cd ../../.. && pwd)
: "${RIMSKY_IMAGE_TAG:?export RIMSKY_IMAGE_TAG=src-<tree hash> first}"
CLI="$ROOT/bin/rimsky"
PEERSRC="$ROOT/.ok-planner/experiments/permissive-peer-build/peer"
NET=exp-enroll-net
STACK=exp-enroll-stack
PEER=exp-enroll-peer
PORT=${PORT:-18541}
PEERPORT=${PEERPORT:-18542}
WORK=$(mktemp -d)
BASE="https://peer.rimsky.internal:$PORT"

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

C=(curl -sS -m 15 --resolve "peer.rimsky.internal:$PORT:127.0.0.1" --cacert "$WORK/ca.pem")
as() { # as <key> <method> <path> [body] -> "<body>\n<status>"
  local k=$1 m=$2 p=$3 b=${4:-}
  "${C[@]}" -w '\n%{http_code}' -X "$m" -H 'content-type: application/json' \
    ${k:+-H "Authorization: Bearer $k"} -H "Idempotency-Key: enroll-$RANDOM" ${b:+-d "$b"} "$BASE$p"; }
scode() { as "$@" | tail -1; }
sbody() { as "$@" | sed '$d'; }

note "== a deployment under mutual-TLS peer auth =="
cat > "$WORK/rimsky.yml" <<'YAML'
persistence:
  driver: sqlite
  sqlite:
    path: /var/lib/rimsky/state.db
peer_auth: mtls
claim_producers: {}
named_locks: {}
executors:
  "standing-service":
    transport: grpc
    endpoint: "standing:9400"
    protocols: ["executor"]
YAML
docker network create "$NET" >/dev/null 2>&1
docker rm -f "$STACK" "$PEER" >/dev/null 2>&1
docker run -d --name "$STACK" --network "$NET" --network-alias rimsky-stack -p "$PORT:8080" \
  -e RIMSKY_CA_ENCRYPTION_KEY="$(python3 -c 'import os,base64;print(base64.b64encode(os.urandom(32)).decode())')" \
  -v "$WORK/rimsky.yml:/etc/rimsky/rimsky.yml:ro" "rimsky-all-in-one:$RIMSKY_IMAGE_TAG" >/dev/null || exit 1
until curl -sk -m 2 "https://127.0.0.1:$PORT/v1/ca-root" -o "$WORK/ca.pem" 2>/dev/null && [ -s "$WORK/ca.pem" ]; do sleep 0.5; done
cp "$WORK/ca.pem" "$WORK/deployment-ca.pem"
ADMIN=$(sbody "" POST /v1/auth/keys '{"name":"admin","permissions":[{"action":"*"}]}' | jq -r .plaintext)
check "deployment locked down with an admin key" yes "$([ -n "$ADMIN" ] && [ "$ADMIN" != null ] && echo yes || echo no)"

note
note "== one key, carrying the enrollment grant and nothing else =="
cat > "$WORK/enroll-only.json" <<'JSON'
{
  "name": "standing-service",
  "description": "A standing service's one credential: it may exchange itself for serving certificates, and do nothing else.",
  "permissions": [{"action": "service:enroll"}]
}
JSON
SVCKEY=$("$CLI" auth create-key --endpoint "https://127.0.0.1:$PORT" --key "$ADMIN" --name standing-service \
  --role-file "$WORK/enroll-only.json" 2>/dev/null | sed -n 's/.*RIMSKY_API_KEY="\(.*\)".*/\1/p')
if [ -z "$SVCKEY" ]; then
  SVCKEY=$(sbody "$ADMIN" POST /v1/auth/keys "$(cat "$WORK/enroll-only.json" | jq -c '{name,permissions}')" | jq -r .plaintext)
fi
check "the service key was minted" yes "$([ -n "$SVCKEY" ] && [ "$SVCKEY" != null ] && echo yes || echo no)"
check "its grant is the enrollment action alone" "service:enroll" \
  "$(sbody "$ADMIN" GET /v1/auth/keys/standing-service | jq -r '[.permissions[].action]|join(",")')"
check "it may enroll" 200 "$(scode "$SVCKEY" POST /v1/enroll '{"label":"probe"}')"
check "it may not read instances" 403 "$(scode "$SVCKEY" GET /v1/instances)"
check "it may not mint keys" 403 "$(scode "$SVCKEY" POST /v1/auth/keys '{"name":"x","permissions":[{"action":"*"}]}')"
check "it may not read the audit log" 403 "$(scode "$SVCKEY" GET /v1/audit)"

note
note "== the service obtains its serving credentials at startup =="
cp -r "$PEERSRC" "$WORK/peer"
sed "s|RIMSKY_PROTOCOLS_PATH|$ROOT/lib/protocols|" "$WORK/peer/go.mod.tmpl" > "$WORK/peer/go.mod"
rm "$WORK/peer/go.mod.tmpl"
( cd "$WORK/peer" && GOOS=linux GOARCH="$(docker info --format '{{.Architecture}}' | sed 's/aarch64/arm64/;s/x86_64/amd64/')" \
    CGO_ENABLED=0 GOFLAGS=-mod=mod GOPROXY=off GOSUMDB=off go build -o "$WORK/peer-linux" . ) || exit 1

start_service() { # start_service <api key>
  docker rm -f "$PEER" >/dev/null 2>&1
  docker run -d --name "$PEER" --network "$NET" --network-alias standing -p "$PEERPORT:9400" \
    -e PEER_PORT=9400 -e PEER_LABEL=standing-service \
    -e RIMSKY_PEER_AUTH=mtls \
    -e RIMSKY_CONTROL_API_URL="https://rimsky-stack:8080" \
    -e RIMSKY_API_KEY="$1" \
    -e RIMSKY_CONTROL_API_CA=/etc/rimsky/deployment-ca.pem \
    -v "$WORK/deployment-ca.pem:/etc/rimsky/deployment-ca.pem:ro" \
    -v "$WORK/peer-linux:/peer:ro" alpine:latest /peer >/dev/null
}
serving_cert() { # serving_cert -> the serving certificate the peer presents, PEM
  openssl s_client -connect "127.0.0.1:$PEERPORT" -servername peer.rimsky.internal \
    -CAfile "$WORK/ca.pem" -tls1_2 -cert "$WORK/client.crt" -key "$WORK/client.key" </dev/null 2>/dev/null \
    | sed -n '/BEGIN CERTIFICATE/,/END CERTIFICATE/p'
}

sbody "$ADMIN" POST /v1/enroll '{"label":"probe-client"}' > "$WORK/client.json"
jq -r .cert_pem "$WORK/client.json" > "$WORK/client.crt"
jq -r .key_pem "$WORK/client.json" > "$WORK/client.key"

start_service "$SVCKEY"
note "waiting for the service to finish enrolling and serve (blocks until it does)"
until [ -n "$(serving_cert)" ]; do
  if [ "$(docker inspect -f '{{.State.Running}}' "$PEER" 2>/dev/null)" != true ]; then
    note "the service exited during startup:"; docker logs "$PEER" 2>&1 | tail -5 | sed 's/^/    /'; break
  fi
  sleep 0.5
done
serving_cert > "$WORK/serving1.pem"
check "the service serves a certificate" yes "$([ -s "$WORK/serving1.pem" ] && echo yes || echo no)"
check "its serving certificate is issued by the deployment CA" "CN=rimsky-deployment-ca" \
  "$(openssl x509 -in "$WORK/serving1.pem" -noout -issuer | sed 's/^issuer=//;s/ //g;s|^/||')"
check "its serving certificate names the key it enrolled with" "$(sbody "$ADMIN" GET /v1/auth/keys/standing-service | jq -r .id)" \
  "$(openssl x509 -in "$WORK/serving1.pem" -noout -subject | sed 's/.*CN *= *//;s|^/CN=||;s/ *$//')"
openssl s_client -connect "127.0.0.1:$PEERPORT" -servername peer.rimsky.internal \
  -CAfile "$WORK/ca.pem" -tls1_2 </dev/null >"$WORK/nocert.txt" 2>&1
NOCERT_RC=$?
check "the credential it obtained also gates its own callers" refused \
  "$([ "$NOCERT_RC" -ne 0 ] && echo refused || echo accepted)"

note
note "== the deployment dispatches work through the enrolled service =="
SPEC='{"tag":"enroll","spec":{"name":"enroll","version":"1","nodes":[{"type":"worker","executor":"standing-service","attributes":{"schema":{"type":"object","properties":{"outcome":{"type":"string","default":"ok"},"echo":{"type":"string","default":"enrolled"}}}}}]}}'
until [ "$(sbody "$ADMIN" GET /v1/observability/executors/standing-service | jq -r '.peer.reachability_status')" = reachable ]; do sleep 0.5; done
TPL=$(sbody "$ADMIN" POST /v1/templates "$SPEC" | jq -r '.template_id // empty')
scode "$ADMIN" POST "/v1/templates/$TPL/deploy" '{}' >/dev/null
IID=$(sbody "$ADMIN" POST /v1/instances "$(printf '{"template":"%s","instance_key":"ck-enroll","params":{}}' "$TPL")" | jq -r '.instance_id // empty')
scode "$ADMIN" POST "/v1/instances/$IID/messages" '{"type":""}' >/dev/null
while :; do
  S=$(sbody "$ADMIN" GET "/v1/observability/nodes/$IID/worker" | jq -r '.run_summary as $r | if $r == null then "in-flight" elif $r.failed_count > 0 then "failed" elif $r.fresh_count > 0 and $r.active_count == 0 and $r.pending_count == 0 then "fresh" else "in-flight" end')
  [ "$S" = in-flight ] || break
  sleep 0.5
done
check "a node settled through the enrolled service" fresh "$S"

note
note "== credentials are short-lived and re-issued from the same key, no operator action =="
NA1=$(jq -r .not_after "$WORK/client.json")
sbody "$SVCKEY" POST /v1/enroll '{"label":"renewal"}' > "$WORK/renew.json"
NA2=$(jq -r .not_after "$WORK/renew.json")
check "a second issuance to the same key succeeded" yes "$([ -n "$NA2" ] && [ "$NA2" != null ] && echo yes || echo no)"
check "each issuance is a distinct credential" yes \
  "$([ "$(jq -r .cert_pem "$WORK/client.json")" != "$(jq -r .cert_pem "$WORK/renew.json")" ] && echo yes || echo no)"
LIFE=$(python3 - "$NA2" <<'PY'
import sys, datetime
import re
na = sys.argv[1]
na = re.sub(r"\.(\d{1,6})\d*", r".\1", na).replace("Z", "+00:00")
delta = datetime.datetime.fromisoformat(na) - datetime.datetime.now(datetime.timezone.utc)
print(int(delta.total_seconds() // 3600))
PY
)
check "the credential is short-lived, so it must be renewed to keep working" yes \
  "$([ "$LIFE" -gt 0 ] && [ "$LIFE" -le 48 ] && echo yes || echo no)"
note "issued credential expires in about ${LIFE}h (first issuance not_after $NA1)"
docker restart "$PEER" >/dev/null
until [ -n "$(serving_cert)" ]; do sleep 0.5; done
serving_cert > "$WORK/serving2.pem"
check "the service re-acquires a credential on its own from the same key" yes \
  "$([ -s "$WORK/serving2.pem" ] && echo yes || echo no)"
check "the operator did nothing between the two acquisitions" yes \
  "$([ "$(openssl x509 -in "$WORK/serving2.pem" -noout -issuer)" = "$(openssl x509 -in "$WORK/serving1.pem" -noout -issuer)" ] && echo yes || echo no)"

note
note "== revoking the one key stops future issuance =="
"$CLI" auth revoke standing-service --endpoint "https://127.0.0.1:$PORT" --key "$ADMIN" >/dev/null 2>&1 \
  || scode "$ADMIN" DELETE /v1/auth/keys/standing-service >/dev/null
check "the revoked key is refused at the enrollment route" 401 "$(scode "$SVCKEY" POST /v1/enroll '{"label":"after-revoke"}')"
start_service "$SVCKEY"
note "waiting for the restarted service to fail closed (blocks until the container exits)"
until [ "$(docker inspect -f '{{.State.Running}}' "$PEER" 2>/dev/null)" != true ]; do sleep 0.5; done
check "a service restarted on the revoked key fails closed" yes \
  "$(docker logs "$PEER" 2>&1 | grep -q 'fail-closed' && echo yes || echo no)"
docker logs "$PEER" 2>&1 | tail -2 | sed 's/^/    /'

note
if [ "$fail" = 0 ]; then note "EXPERIMENT PASS"; else note "EXPERIMENT FAIL"; fi
exit "$fail"
