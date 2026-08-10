#!/usr/bin/env bash
# Experiment: peer-tls-enforced
#
# The `tls:` key on a peer entry is driven both ways against the same peer
# process, so the key is the only thing that differs:
#
#   plaintext peer, tls: off       -> reachable, reported tls=off
#   plaintext peer, tls: required  -> refused, loud, and dispatches to it fail
#   the same, on a claim-producer  -> refused the same way on the store side
#   TLS-serving peer, tls:required -> reachable, reported tls=required, the
#                                     certificate verifies against the
#                                     deployment CA, and a node runs through it
#
# The peer is the third-party peer built for permissive-peer-build: one binary,
# plaintext or mutually-authenticated depending on its environment.

set -u
cd "$(dirname "$0")"
ROOT=$(cd ../../.. && pwd)
: "${RIMSKY_IMAGE_TAG:?export RIMSKY_IMAGE_TAG=src-<tree hash> first}"
PEERSRC="$ROOT/.ok-planner/experiments/permissive-peer-build/peer"
NET=exp-tls-net
PSTACK=exp-tls-plain-stack
PPEER=exp-tls-plain-peer
MSTACK=exp-tls-mtls-stack
MPEER=exp-tls-mtls-peer
SSTACK=exp-tls-store-stack
PPORT=${PPORT:-18561}
MPORT=${MPORT:-18562}
MPEERPORT=${MPEERPORT:-18563}
WORK=$(mktemp -d)
PBASE="http://127.0.0.1:$PPORT"
MBASE="https://peer.rimsky.internal:$MPORT"

fail=0
note() { printf '%s\n' "$*"; }
check() {
  if [ "$2" = "$3" ]; then printf 'PASS  %-62s %s\n' "$1" "$3"
  else printf 'FAIL  %-62s expected [%s] got [%s]\n' "$1" "$2" "$3"; fail=1; fi
}
cleanup() {
  docker rm -f "$PSTACK" "$PPEER" "$MSTACK" "$MPEER" "$SSTACK" >/dev/null 2>&1
  docker network rm "$NET" >/dev/null 2>&1
  rm -rf "$WORK"
}
trap cleanup EXIT

p() { local m=$1 pa=$2 b=${3:-}
  curl -sS -m 15 -X "$m" -H 'content-type: application/json' -H "Idempotency-Key: tls-$RANDOM" ${b:+-d "$b"} "$PBASE$pa"; }
M=(curl -sS -m 15 --resolve "peer.rimsky.internal:$MPORT:127.0.0.1" --cacert "$WORK/ca.pem")
m() { local mm=$1 pa=$2 b=${3:-}
  "${M[@]}" -X "$mm" -H 'content-type: application/json' -H "Authorization: Bearer $MADMIN" \
    -H "Idempotency-Key: tls-$RANDOM" ${b:+-d "$b"} "$MBASE$pa"; }

note "== build the peer, and the two stacks that consume it =="
cp -r "$PEERSRC" "$WORK/peer"
sed "s|RIMSKY_PROTOCOLS_PATH|$ROOT/lib/protocols|" "$WORK/peer/go.mod.tmpl" > "$WORK/peer/go.mod"
rm "$WORK/peer/go.mod.tmpl"
( cd "$WORK/peer" && GOOS=linux GOARCH="$(docker info --format '{{.Architecture}}' | sed 's/aarch64/arm64/;s/x86_64/amd64/')" \
    CGO_ENABLED=0 GOFLAGS=-mod=mod GOPROXY=off GOSUMDB=off go build -o "$WORK/peer-linux" . ) || exit 1
check "peer built" yes yes

cat > "$WORK/plain.yml" <<'YAML'
persistence:
  driver: sqlite
  sqlite:
    path: /var/lib/rimsky/state.db
claim_producers: {}
named_locks: {}
executors:
  "plain-off":
    transport: grpc
    endpoint: "plainpeer:9400"
    tls: "off"
    protocols: ["executor"]
  "plain-required":
    transport: grpc
    endpoint: "plainpeer:9400"
    tls: "required"
    protocols: ["executor"]
YAML
docker network create "$NET" >/dev/null 2>&1
docker rm -f "$PSTACK" "$PPEER" "$MSTACK" "$MPEER" "$SSTACK" >/dev/null 2>&1
docker run -d --name "$PPEER" --network "$NET" --network-alias plainpeer \
  -e PEER_PORT=9400 -e PEER_LABEL=plaintext-peer -v "$WORK/peer-linux:/peer:ro" alpine:latest /peer >/dev/null || exit 1
docker run -d --name "$PSTACK" --network "$NET" -p "$PPORT:8080" \
  -v "$WORK/plain.yml:/etc/rimsky/rimsky.yml:ro" "rimsky-all-in-one:$RIMSKY_IMAGE_TAG" >/dev/null || exit 1
until [ "$(curl -sS -m 3 -o /dev/null -w '%{http_code}' "$PBASE/v1/health" 2>/dev/null)" = 200 ]; do sleep 0.5; done

note
note "== one peer process, two tls settings =="
note "waiting for the tls: off entry to be probed (blocks until it is)"
until [ "$(p GET /v1/observability/executors/plain-off | jq -r '.peer.reachability_status')" = reachable ]; do sleep 0.5; done
check "tls: off reaches the plaintext peer" reachable "$(p GET /v1/observability/executors/plain-off | jq -r '.peer.reachability_status')"
check "and reports the setting it was given" off "$(p GET /v1/observability/executors/plain-off | jq -r '.peer.tls')"

note "waiting for the tls: required entry to be probed (blocks until it is)"
until [ -n "$(p GET /v1/observability/executors/plain-required | jq -r '.peer.last_error // empty')" ]; do sleep 0.5; done
REQVIEW=$(p GET /v1/observability/executors/plain-required)
check "tls: required refuses the same plaintext peer" unreachable "$(printf '%s' "$REQVIEW" | jq -r '.peer.reachability_status')"
check "and reports the setting it was given" required "$(printf '%s' "$REQVIEW" | jq -r '.peer.tls')"
LASTERR=$(printf '%s' "$REQVIEW" | jq -r '.peer.last_error')
note "reported failure: $LASTERR"
check "the failure names the peer and the setting" yes \
  "$(printf '%s' "$LASTERR" | grep -q 'plain-required' && printf '%s' "$LASTERR" | grep -q 'tls: required' && echo yes || echo no)"

note
note "== the same key on the store side =="
cat > "$WORK/store.yml" <<'YAML'
persistence:
  driver: sqlite
  sqlite:
    path: /var/lib/rimsky/state.db
claim_producers:
  "store-required":
    endpoint: "plainpeer:9400"
    tls: "required"
    protocols: ["claim_producer"]
    write_semantics_allowed: ["read_only"]
named_locks: {}
executors: {}
YAML
docker rm -f "$SSTACK" >/dev/null 2>&1
docker run -d --name "$SSTACK" --network "$NET" \
  -v "$WORK/store.yml:/etc/rimsky/rimsky.yml:ro" "rimsky-all-in-one:$RIMSKY_IMAGE_TAG" >/dev/null || exit 1
note "waiting for the store-side stack to settle (blocks until it stops running)"
until [ "$(docker inspect -f '{{.State.Running}}' "$SSTACK" 2>/dev/null)" != true ]; do sleep 0.5; done
SLOG=$(docker logs "$SSTACK" 2>&1)
check "a store that cannot present credentials stops the stack starting" 1 \
  "$(docker inspect -f '{{.State.ExitCode}}' "$SSTACK")"
note "reported failure: $(printf '%s' "$SLOG" | grep -m1 'store-required' | tail -c 260)"
check "the store failure names the peer and the setting" yes \
  "$(printf '%s' "$SLOG" | grep -q 'store-required' && printf '%s' "$SLOG" | grep -q 'tls: required' && echo yes || echo no)"

note
note "== the refusal is loud where the work is, too =="
# No attributes block: an unreachable executor has no schema to validate a
# declared one against, and this template must register whether or not the peer
# its node names can be reached.
SPEC_TMPL='{"tag":"%s","spec":{"name":"%s","version":"1","nodes":[{"type":"worker","executor":"%s"}]}}'
drive_plain() { # drive_plain <executor name> -> "<settled state> <error kinds>"
  local ex=$1 spec tpl iid s
  spec=$(printf "$SPEC_TMPL" "$ex" "$ex" "$ex")
  tpl=$(p POST /v1/templates "$spec" | jq -r '.template_id // empty')
  [ -n "$tpl" ] || { echo "no-template"; return; }
  p POST "/v1/templates/$tpl/deploy" '{}' >/dev/null
  iid=$(p POST /v1/instances "$(printf '{"template":"%s","instance_key":"ck-%s","params":{},"target_agent":"tls-probe-agent"}' "$tpl" "$ex")" | jq -r '.instance_id // empty')
  [ -n "$iid" ] || { echo "no-instance"; return; }
  p POST "/v1/instances/$iid/messages" '{"type":""}' >/dev/null
  while :; do
    s=$(p GET "/v1/observability/nodes/$iid/worker" | jq -r '.run_summary as $r | if $r == null then "in-flight" elif $r.failed_count > 0 then "failed" elif $r.fresh_count > 0 and $r.active_count == 0 and $r.pending_count == 0 then "fresh" else "in-flight" end')
    [ "$s" = in-flight ] || break
    sleep 0.5
  done
  printf '%s %s' "$s" "$(p GET "/v1/observability/nodes/$iid/worker" | jq -r '[.events[].kind]|map(select(startswith("terminal/")))|unique|join(",")')"
}
note "driving a node at the tls: off entry (blocks until it settles)"
check "work runs through the peer when tls is off" "fresh terminal/success" "$(drive_plain plain-off)"
note "driving a node at the tls: required entry (blocks until it settles)"
REQRESULT=$(drive_plain plain-required)
note "settled: $REQRESULT"
check "work fails at the same peer when tls is required" failed "$(printf '%s' "$REQRESULT" | awk '{print $1}')"

note
note "== a peer that can present credentials =="
cat > "$WORK/mtls.yml" <<'YAML'
persistence:
  driver: sqlite
  sqlite:
    path: /var/lib/rimsky/state.db
peer_auth: mtls
claim_producers: {}
named_locks: {}
executors:
  "secure-required":
    transport: grpc
    endpoint: "securepeer:9400"
    tls: "required"
    protocols: ["executor"]
YAML
docker run -d --name "$MSTACK" --network "$NET" --network-alias rimsky-mtls -p "$MPORT:8080" \
  -e RIMSKY_CA_ENCRYPTION_KEY="$(python3 -c 'import os,base64;print(base64.b64encode(os.urandom(32)).decode())')" \
  -v "$WORK/mtls.yml:/etc/rimsky/rimsky.yml:ro" "rimsky-all-in-one:$RIMSKY_IMAGE_TAG" >/dev/null || exit 1
until curl -sk -m 2 "https://127.0.0.1:$MPORT/v1/ca-root" -o "$WORK/ca.pem" 2>/dev/null && [ -s "$WORK/ca.pem" ]; do sleep 0.5; done
MADMIN=""
MADMIN=$("${M[@]}" -X POST -H 'content-type: application/json' -d '{"name":"admin","permissions":[{"action":"*"}]}' "$MBASE/v1/auth/keys" | jq -r .plaintext)
cp "$WORK/ca.pem" "$WORK/deployment-ca.pem"
docker run -d --name "$MPEER" --network "$NET" --network-alias securepeer -p "$MPEERPORT:9400" \
  -e PEER_PORT=9400 -e PEER_LABEL=secure-peer \
  -e RIMSKY_PEER_AUTH=mtls \
  -e RIMSKY_CONTROL_API_URL="https://rimsky-mtls:8080" \
  -e RIMSKY_API_KEY="$MADMIN" \
  -e RIMSKY_CONTROL_API_CA=/etc/rimsky/deployment-ca.pem \
  -v "$WORK/deployment-ca.pem:/etc/rimsky/deployment-ca.pem:ro" \
  -v "$WORK/peer-linux:/peer:ro" alpine:latest /peer >/dev/null || exit 1
note "waiting for the credentialed peer to be reachable (blocks until it is)"
until [ "$(m GET /v1/observability/executors/secure-required | jq -r '.peer.reachability_status')" = reachable ]; do sleep 0.5; done
check "tls: required reaches a peer that can present credentials" reachable \
  "$(m GET /v1/observability/executors/secure-required | jq -r '.peer.reachability_status')"
check "and reports the setting it was given" required "$(m GET /v1/observability/executors/secure-required | jq -r '.peer.tls')"
m POST /v1/enroll '{"label":"tls-probe"}' > "$WORK/probe.json"
jq -r .cert_pem "$WORK/probe.json" > "$WORK/probe.crt"
jq -r .key_pem "$WORK/probe.json" > "$WORK/probe.key"
check "the peer's certificate verifies against the deployment CA" "CN=rimsky-deployment-ca" \
  "$(openssl s_client -connect "127.0.0.1:$MPEERPORT" -servername peer.rimsky.internal -CAfile "$WORK/ca.pem" \
     -tls1_2 -cert "$WORK/probe.crt" -key "$WORK/probe.key" </dev/null 2>/dev/null \
     | openssl x509 -noout -issuer 2>/dev/null | sed 's/^issuer=//;s/ //g;s|^/||')"
MSPEC=$(printf "$SPEC_TMPL" secure secure secure-required)
MTPL=$(m POST /v1/templates "$MSPEC" | jq -r '.template_id // empty')
m POST "/v1/templates/$MTPL/deploy" '{}' >/dev/null
MIID=$(m POST /v1/instances "$(printf '{"template":"%s","instance_key":"ck-secure","params":{}}' "$MTPL")" | jq -r '.instance_id // empty')
m POST "/v1/instances/$MIID/messages" '{"type":""}' >/dev/null
note "driving a node over the verified connection (blocks until it settles)"
while :; do
  MS=$(m GET "/v1/observability/nodes/$MIID/worker" | jq -r '.run_summary as $r | if $r == null then "in-flight" elif $r.failed_count > 0 then "failed" elif $r.fresh_count > 0 and $r.active_count == 0 and $r.pending_count == 0 then "fresh" else "in-flight" end')
  [ "$MS" = in-flight ] || break
  sleep 0.5
done
check "work runs over the TLS-verified connection" fresh "$MS"
check "and the peer that served it is the credentialed one" secure-peer \
  "$(m GET "/v1/observability/nodes/$MIID/worker" | jq -r '.latest_attributes.served_by // empty')"

note
if [ "$fail" = 0 ]; then note "EXPERIMENT PASS"; else note "EXPERIMENT FAIL"; fi
exit "$fail"
