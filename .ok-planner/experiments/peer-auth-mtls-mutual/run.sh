#!/usr/bin/env bash
# Experiment: peer-auth-mtls-mutual
#
# The operator flips one config key (`peer_auth: mtls`) and supplies the CA
# encryption key, and nothing else. The run then checks what that flip bought:
#   - the control-API listener stops answering plaintext and serves a CA-signed
#     certificate
#   - a bundled executor brought up under the same flag enrolls and serves a
#     listener that REFUSES a client with no certificate and a client holding a
#     certificate from another CA, and accepts one holding a deployment leaf
#   - the executor entry declares no `tls:` key, yet the stack reports it at
#     tls=required and drives a node through it to terminal
# and what it costs when it is off:
#   - the same image with the default `none` boots and drives a node to terminal
#     against a plaintext peer, with no CA, no key, and no certificates
set -u
cd "$(dirname "$0")"
ROOT=$(cd ../../.. && pwd)
: "${RIMSKY_IMAGE_TAG:?export RIMSKY_IMAGE_TAG=src-<tree hash> first}"
PEERSRC="$ROOT/.ok-planner/experiments/permissive-peer-build/peer"
NET=exp-mtls-net
STACK=exp-mtls-stack
EXEC=exp-mtls-exec
PLAINSTACK=exp-mtls-plain-stack
PLAINPEER=exp-mtls-plain-peer
PORT=${PORT:-18531}
EXECPORT=${EXECPORT:-18532}
PLAINPORT=${PLAINPORT:-18533}
WORK=$(mktemp -d)
BASE="https://peer.rimsky.internal:$PORT"
PLAINBASE="http://127.0.0.1:$PLAINPORT"

fail=0
note() { printf '%s\n' "$*"; }
check() {
  if [ "$2" = "$3" ]; then printf 'PASS  %-62s %s\n' "$1" "$3"
  else printf 'FAIL  %-62s expected [%s] got [%s]\n' "$1" "$2" "$3"; fail=1; fi
}
cleanup() {
  docker rm -f "$STACK" "$EXEC" "$PLAINSTACK" "$PLAINPEER" >/dev/null 2>&1
  docker network rm "$NET" >/dev/null 2>&1
  rm -rf "$WORK"
}
trap cleanup EXIT

C=(curl -sS -m 15 --resolve "peer.rimsky.internal:$PORT:127.0.0.1" --cacert "$WORK/ca.pem")
req() { local m=$1 p=$2 b=${3:-}
  "${C[@]}" -w '\n%{http_code}' -X "$m" -H 'content-type: application/json' -H "Authorization: Bearer $ADMIN" \
    -H "Idempotency-Key: mtls-$RANDOM" ${b:+-d "$b"} "$BASE$p"; }
code() { req "$@" | tail -1; }
body() { req "$@" | sed '$d'; }
pcode() { local m=$1 p=$2 b=${3:-}
  curl -sS -m 15 -o /dev/null -w '%{http_code}' -X "$m" -H 'content-type: application/json' \
    -H "Idempotency-Key: plain-$RANDOM" ${b:+-d "$b"} "$PLAINBASE$p"; }
pbody() { local m=$1 p=$2 b=${3:-}
  curl -sS -m 15 -X "$m" -H 'content-type: application/json' -H "Idempotency-Key: plain-$RANDOM" ${b:+-d "$b"} "$PLAINBASE$p"; }

note "== the one flip =="
cat > "$WORK/rimsky.yml" <<'YAML'
persistence:
  driver: sqlite
  sqlite:
    path: /var/lib/rimsky/state.db
peer_auth: mtls
claim_producers: {}
named_locks: {}
executors:
  "http-node-remote":
    transport: grpc
    endpoint: "exec-mtls:9091"
    protocols: ["executor"]
YAML
docker network create "$NET" >/dev/null 2>&1
docker rm -f "$STACK" "$EXEC" "$PLAINSTACK" "$PLAINPEER" >/dev/null 2>&1
docker run -d --name "$STACK" --network "$NET" --network-alias rimsky-stack -p "$PORT:8080" \
  -e RIMSKY_CA_ENCRYPTION_KEY="$(python3 -c 'import os,base64;print(base64.b64encode(os.urandom(32)).decode())')" \
  -v "$WORK/rimsky.yml:/etc/rimsky/rimsky.yml:ro" "rimsky-all-in-one:$RIMSKY_IMAGE_TAG" >/dev/null || exit 1
until curl -sk -m 2 "https://127.0.0.1:$PORT/v1/ca-root" -o "$WORK/ca.pem" 2>/dev/null && [ -s "$WORK/ca.pem" ]; do sleep 0.5; done

ADMIN=""
check "the control-API listener refuses plaintext HTTP" 400 \
  "$(curl -sS -m 10 -o /dev/null -w '%{http_code}' "http://127.0.0.1:$PORT/v1/health")"
check "the control-API certificate verifies against the deployment CA" 200 \
  "$("${C[@]}" -o /dev/null -w '%{http_code}' "$BASE/v1/health")"
check "the control-API certificate is issued by the deployment CA" "CN=rimsky-deployment-ca" \
  "$(openssl s_client -connect "127.0.0.1:$PORT" -servername peer.rimsky.internal </dev/null 2>/dev/null | openssl x509 -noout -issuer | sed 's/^issuer=//;s/ //g;s|^/||')"

ADMIN=$("${C[@]}" -X POST -H 'content-type: application/json' -d '{"name":"admin","permissions":[{"action":"*"}]}' "$BASE/v1/auth/keys" | jq -r .plaintext)
check "an admin key was minted over the TLS listener" yes "$([ -n "$ADMIN" ] && [ "$ADMIN" != null ] && echo yes || echo no)"

note
note "== a bundled service comes up under the same flag =="
cp "$WORK/ca.pem" "$WORK/deployment-ca.pem"
docker run -d --name "$EXEC" --network "$NET" --network-alias exec-mtls -p "$EXECPORT:9091" \
  -e RIMSKY_EXECUTOR_PORT_GRPC=9091 -e RIMSKY_EXECUTOR_PORT_HTTP=9092 \
  -e RIMSKY_EXECUTOR_STUB_MODE=1 \
  -e RIMSKY_PEER_AUTH=mtls \
  -e RIMSKY_CONTROL_API_URL="https://rimsky-stack:8080" \
  -e RIMSKY_API_KEY="$ADMIN" \
  -e RIMSKY_CONTROL_API_CA=/etc/rimsky/deployment-ca.pem \
  -v "$WORK/deployment-ca.pem:/etc/rimsky/deployment-ca.pem:ro" \
  "rimsky-executor-http-node:$RIMSKY_IMAGE_TAG" >/dev/null || exit 1

note "waiting for the stack to report the executor reachable (blocks until it does)"
until [ "$(body GET /v1/observability/executors/http-node-remote | jq -r '.peer.reachability_status')" = reachable ]; do sleep 0.5; done
check "the executor entry declared no tls key, yet the flip made it required" required \
  "$(body GET /v1/observability/executors/http-node-remote | jq -r '.peer.tls')"

note
note "== the executor's listener is mutually authenticated =="
openssl s_client -connect "127.0.0.1:$EXECPORT" -servername peer.rimsky.internal \
  -CAfile "$WORK/ca.pem" -tls1_2 </dev/null >"$WORK/nocert.txt" 2>&1
NOCERT_RC=$?
check "a client with no certificate is refused" refused "$([ "$NOCERT_RC" -ne 0 ] && echo refused || echo accepted)"

openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 1 \
  -keyout "$WORK/imp-ca.key" -out "$WORK/imp-ca.crt" -subj "/CN=impostor-ca" >/dev/null 2>&1
openssl req -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes \
  -keyout "$WORK/imp.key" -out "$WORK/imp.csr" -subj "/CN=key-impostor" >/dev/null 2>&1
openssl x509 -req -in "$WORK/imp.csr" -CA "$WORK/imp-ca.crt" -CAkey "$WORK/imp-ca.key" \
  -out "$WORK/imp.crt" -days 1 -set_serial 1 >/dev/null 2>&1
openssl s_client -connect "127.0.0.1:$EXECPORT" -servername peer.rimsky.internal \
  -CAfile "$WORK/ca.pem" -tls1_2 -cert "$WORK/imp.crt" -key "$WORK/imp.key" </dev/null >"$WORK/imp.txt" 2>&1
IMP_RC=$?
check "a client holding another CA's certificate is refused" refused "$([ "$IMP_RC" -ne 0 ] && echo refused || echo accepted)"

body POST /v1/enroll '{"label":"probe"}' > "$WORK/enroll.json"
jq -r .cert_pem "$WORK/enroll.json" > "$WORK/leaf.crt"
jq -r .key_pem "$WORK/enroll.json" > "$WORK/leaf.key"
check "the deployment issued this probe a leaf" yes "$([ -s "$WORK/leaf.crt" ] && [ -s "$WORK/leaf.key" ] && echo yes || echo no)"
openssl s_client -connect "127.0.0.1:$EXECPORT" -servername peer.rimsky.internal \
  -CAfile "$WORK/ca.pem" -tls1_2 -cert "$WORK/leaf.crt" -key "$WORK/leaf.key" </dev/null >"$WORK/leaf.txt" 2>&1
LEAF_RC=$?
check "a client holding a deployment leaf completes the handshake" accepted "$([ "$LEAF_RC" -eq 0 ] && echo accepted || echo refused)"
check "the executor presented a deployment-signed certificate" "CN=rimsky-deployment-ca" \
  "$(sed -n '/Certificate chain/,/---/p' "$WORK/leaf.txt" | grep -m1 'i:' | sed 's/.*i://;s/^\///;s/ //g')"

note
note "== the forward dispatch leg still works =="
SPEC='{"tag":"mtls","spec":{"name":"mtls","version":"1","nodes":[{"type":"worker","executor":"http-node-remote","attributes":{"schema":{"type":"object","properties":{"stub_probe":{"type":"boolean","default":true},"stub":{"type":"boolean","default":false}}}}}]}}'
TPL=$(body POST /v1/templates "$SPEC" | jq -r '.template_id // empty')
check "template registered over the TLS control API" yes "$([ -n "$TPL" ] && echo yes || echo no)"
check "template deployed" 200 "$(code POST "/v1/templates/$TPL/deploy" '{}')"
CREATE=$(printf '{"template":"%s","instance_key":"ck-mtls","params":{}}' "$TPL")
IID=$(body POST /v1/instances "$CREATE" | jq -r '.instance_id // empty')
check "instance created" yes "$([ -n "$IID" ] && echo yes || echo no)"
code POST "/v1/instances/$IID/messages" '{"type":""}' >/dev/null
note "waiting for the node to settle over the mutually authenticated leg"
while :; do
  S=$(body GET "/v1/observability/nodes/$IID/worker" | jq -r '.run_summary as $r | if $r == null then "in-flight" elif $r.failed_count > 0 then "failed" elif $r.fresh_count > 0 and $r.active_count == 0 and $r.pending_count == 0 then "fresh" else "in-flight" end')
  [ "$S" = in-flight ] || break
  sleep 0.5
done
check "the node settled fresh through the mTLS executor" fresh "$S"

note
note "== the default costs nothing =="
cp -r "$PEERSRC" "$WORK/peer"
sed "s|RIMSKY_PROTOCOLS_PATH|$ROOT/lib/protocols|" "$WORK/peer/go.mod.tmpl" > "$WORK/peer/go.mod"
rm "$WORK/peer/go.mod.tmpl"
( cd "$WORK/peer" && GOOS=linux GOARCH="$(docker info --format '{{.Architecture}}' | sed 's/aarch64/arm64/;s/x86_64/amd64/')" \
    CGO_ENABLED=0 GOFLAGS=-mod=mod GOPROXY=off GOSUMDB=off go build -o "$WORK/peer-linux" . ) || exit 1
cat > "$WORK/plain.yml" <<'YAML'
persistence:
  driver: sqlite
  sqlite:
    path: /var/lib/rimsky/state.db
claim_producers: {}
named_locks: {}
executors:
  "peer":
    transport: grpc
    endpoint: "plain-peer:9400"
    protocols: ["executor"]
YAML
docker run -d --name "$PLAINPEER" --network "$NET" --network-alias plain-peer \
  -e PEER_PORT=9400 -e PEER_LABEL=plain-peer -v "$WORK/peer-linux:/peer:ro" alpine:latest /peer >/dev/null || exit 1
docker run -d --name "$PLAINSTACK" --network "$NET" -p "$PLAINPORT:8080" \
  -v "$WORK/plain.yml:/etc/rimsky/rimsky.yml:ro" "rimsky-all-in-one:$RIMSKY_IMAGE_TAG" >/dev/null || exit 1
until [ "$(pcode GET /v1/health)" = 200 ]; do sleep 0.5; done
check "the default stack answers plaintext with no CA and no certificates" 200 "$(pcode GET /v1/health)"
until [ "$(pbody GET /v1/observability/executors/peer | jq -r '.peer.reachability_status')" = reachable ]; do sleep 0.5; done
check "its peer is reachable with tls off" off "$(pbody GET /v1/observability/executors/peer | jq -r '.peer.tls')"
PSPEC='{"tag":"plain","spec":{"name":"plain","version":"1","nodes":[{"type":"worker","executor":"peer","attributes":{"schema":{"type":"object","properties":{"outcome":{"type":"string","default":"ok"},"echo":{"type":"string","default":"plain"}}}}}]}}'
PTPL=$(pbody POST /v1/templates "$PSPEC" | jq -r '.template_id // empty')
pcode POST "/v1/templates/$PTPL/deploy" '{}' >/dev/null
PIID=$(pbody POST /v1/instances "$(printf '{"template":"%s","instance_key":"ck-plain","params":{},"target_agent":"plain-probe-agent"}' "$PTPL")" | jq -r '.instance_id // empty')
pcode POST "/v1/instances/$PIID/messages" '{"type":""}' >/dev/null
while :; do
  PS=$(pbody GET "/v1/observability/nodes/$PIID/worker" | jq -r '.run_summary as $r | if $r == null then "in-flight" elif $r.failed_count > 0 then "failed" elif $r.fresh_count > 0 and $r.active_count == 0 and $r.pending_count == 0 then "fresh" else "in-flight" end')
  [ "$PS" = in-flight ] || break
  sleep 0.5
done
check "the default stack drives a node to terminal unchanged" fresh "$PS"

note
if [ "$fail" = 0 ]; then note "EXPERIMENT PASS"; else note "EXPERIMENT FAIL"; fi
exit "$fail"
