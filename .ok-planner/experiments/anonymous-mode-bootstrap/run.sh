#!/usr/bin/env bash
# Experiment: anonymous-mode-bootstrap
#
# Drives a fresh rimsky deployment through the public surface only:
#   1. anonymous mode is open  -> every ruled control-API route answers without a token
#   2. machine service enrollment is the one exception -> POST /v1/enroll is refused
#   3. `rimsky auth init` mints the first admin key
#   4. anonymous mode is closed -> the same unauthenticated sweep is refused
#   5. the minted key restores access, enrollment included
#
# Two stacks, because two of the 83 ruled control-API routes (POST /v1/enroll,
# GET /v1/ca-root) are mounted only when a deployment CA exists:
#   MAIN  - the zero-config `rimsky-all-in-one` default, plain HTTP
#   MTLS  - the same image with `peer_auth: mtls`, which stands up the CA
#
# Instruments: the `rimsky-all-in-one` image, the `rimsky` CLI binary, curl.

set -u
cd "$(dirname "$0")"
ROOT=$(cd ../../.. && pwd)
: "${RIMSKY_IMAGE_TAG:?export RIMSKY_IMAGE_TAG=src-<tree hash> first}"
CLI="$ROOT/bin/rimsky"
PORT=${PORT:-18461}
MPORT=${MPORT:-18462}
NAME=exp-anon-bootstrap
MNAME=exp-anon-bootstrap-mtls
WORK=$(mktemp -d)
BASE="http://127.0.0.1:$PORT"
MBASE="https://peer.rimsky.internal:$MPORT"
CURL=(curl -sS -m 10)
MCURL=(curl -sS -m 10 --resolve "peer.rimsky.internal:$MPORT:127.0.0.1" --cacert "$WORK/ca.pem")
export HOME="$WORK"
unset RIMSKY_API_KEY RIMSKY_CONTROL_API_URL 2>/dev/null

fail=0
note() { printf '%s\n' "$*"; }
check() { # check <label> <expected> <actual>
  if [ "$2" = "$3" ]; then printf 'PASS  %-62s %s\n' "$1" "$3"
  else printf 'FAIL  %-62s expected [%s] got [%s]\n' "$1" "$2" "$3"; fail=1; fi
}

cleanup() { docker rm -f "$NAME" "$MNAME" >/dev/null 2>&1; rm -rf "$WORK"; }
trap cleanup EXIT

cat > "$WORK/rimsky-mtls.yml" <<'YAML'
persistence:
  driver: sqlite
  sqlite:
    path: /var/lib/rimsky/state.db
peer_auth: mtls
claim_producers: {}
named_locks: {}
executors: {}
YAML

docker rm -f "$NAME" "$MNAME" >/dev/null 2>&1
docker run -d --name "$NAME" -p "$PORT:8080" "rimsky-all-in-one:$RIMSKY_IMAGE_TAG" >/dev/null || exit 1
docker run -d --name "$MNAME" -p "$MPORT:8080" \
  -e RIMSKY_CA_ENCRYPTION_KEY="$(python3 -c 'import os,base64;print(base64.b64encode(os.urandom(32)).decode())')" \
  -v "$WORK/rimsky-mtls.yml:/etc/rimsky/rimsky.yml:ro" \
  "rimsky-all-in-one:$RIMSKY_IMAGE_TAG" >/dev/null || exit 1

until [ "$("${CURL[@]}" -o /dev/null -w '%{http_code}' "$BASE/v1/health" 2>/dev/null)" = 200 ]; do sleep 0.5; done
# The CA root is served unauthenticated by design; it is also how this script
# gets a verified TLS channel to the MTLS stack.
until curl -sk -m 2 "https://127.0.0.1:$MPORT/v1/ca-root" -o "$WORK/ca.pem" 2>/dev/null && [ -s "$WORK/ca.pem" ]; do sleep 0.5; done

status_of() { # status_of METHOD PATH  -- unauthenticated, against MAIN
  local m=$1 p=$2
  case "$m" in
    GET|DELETE) "${CURL[@]}" -o /dev/null -w '%{http_code}' -X "$m" "$BASE$p" ;;
    POST|PUT)   "${CURL[@]}" -o /dev/null -w '%{http_code}' -X "$m" -H 'content-type: application/json' \
                  -H 'Idempotency-Key: exp-anon' -d '{}' "$BASE$p" ;;
  esac
}

sweep() { # sweep <outfile>
  : > "$1"
  while IFS=$'\t' read -r m ruled concrete; do
    [ -z "${m:-}" ] && continue
    printf '%s\t%s\t%s\n' "$m" "$ruled" "$(status_of "$m" "$concrete")" >> "$1"
  done < <(grep -v $'\t/v1/enroll\t\|\t/v1/ca-root\t' routes.tsv)
}

TOTAL=$(grep -c . routes.tsv)
MAINROUTES=$(grep -vc $'\t/v1/enroll\t\|\t/v1/ca-root\t' routes.tsv)
note "== population =="
note "ruled control-API routes in the surface ruling: $TOTAL"
note "  swept against MAIN: $MAINROUTES"
note "  exercised against MTLS (mounted only with a deployment CA): POST /v1/enroll, GET /v1/ca-root"

note
note "== phase 1: anonymous mode is open =="
check "MAIN auth status mode" anonymous "$("${CURL[@]}" "$BASE/v1/auth/status" | jq -r .mode)"
check "MTLS auth status mode" anonymous "$("${MCURL[@]}" "$MBASE/v1/auth/status" | jq -r .mode)"
sweep "$WORK/anon.tsv"
check "MAIN routes refused (401/403) in anonymous mode" "" "$(awk -F'\t' '$3==401 || $3==403' "$WORK/anon.tsv")"
note "MAIN anonymous status distribution:"
awk -F'\t' '{print $3}' "$WORK/anon.tsv" | sort | uniq -c | sort -rn | sed 's/^/    /'
check "MTLS GET /v1/ca-root anonymous" 200 "$("${MCURL[@]}" -o /dev/null -w '%{http_code}' "$MBASE/v1/ca-root")"
check "MTLS POST /v1/enroll anonymous (the one exception)" 403 \
  "$("${MCURL[@]}" -o /dev/null -w '%{http_code}' -X POST -H 'content-type: application/json' -d '{"label":"exp"}' "$MBASE/v1/enroll")"

note
note "== phase 1b: a real operator lifecycle, unauthenticated =="
TPL=$("${CURL[@]}" -X POST -H 'content-type: application/json' \
  -d '{"tag":"anon-bootstrap","spec":{"name":"anon-bootstrap","version":"1","nodes":[{"type":"worker","executor":"http-node"}]}}' \
  "$BASE/v1/templates" | jq -r .template_id)
check "POST /v1/templates minted a template id" yes "$([ -n "$TPL" ] && [ "$TPL" != null ] && echo yes || echo no)"
check "POST /v1/templates/{id}/deploy" 200 "$("${CURL[@]}" -o /dev/null -w '%{http_code}' -X POST -H 'content-type: application/json' -d '{}' "$BASE/v1/templates/$TPL/deploy")"
INST=$("${CURL[@]}" -X POST -H 'content-type: application/json' \
  -d "{\"template\":\"$TPL\",\"instance_key\":\"ck-anon-bootstrap\",\"params\":{},\"target_agent\":\"anon-agent\"}" \
  "$BASE/v1/instances" | jq -r .instance_id)
check "POST /v1/instances minted an instance id" yes "$([ -n "$INST" ] && [ "$INST" != null ] && echo yes || echo no)"
check "GET /v1/instances/{id}" 200 "$("${CURL[@]}" -o /dev/null -w '%{http_code}' "$BASE/v1/instances/$INST")"
check "POST /v1/instances/{id}/terminate" 200 "$("${CURL[@]}" -o /dev/null -w '%{http_code}' -X POST -H 'content-type: application/json' -d '{}' "$BASE/v1/instances/$INST/terminate")"

note
note "== phase 2: mint the first admin key with the CLI =="
INIT=$("$CLI" auth init --endpoint "$BASE" 2>&1)
printf '%s\n' "$INIT" | sed 's/^/    /'
ADMIN=$(printf '%s\n' "$INIT" | sed -n 's/.*RIMSKY_API_KEY="\(.*\)".*/\1/p')
check "rimsky auth init surfaced a plaintext key" yes "$([ -n "$ADMIN" ] && echo yes || echo no)"

note
note "== phase 3: anonymous mode is closed =="
sweep "$WORK/locked.tsv"
note "MAIN routes still answering unauthenticated:"
awk -F'\t' '$3!=401' "$WORK/locked.tsv" | sed 's/^/    /'
check "only the liveness probe still answers unauthenticated" "$(printf 'GET\t/v1/health\t200')" "$(awk -F'\t' '$3!=401' "$WORK/locked.tsv")"
check "every other swept route refused with 401" "$(( MAINROUTES - 1 ))" "$(awk -F'\t' '$3==401' "$WORK/locked.tsv" | wc -l | tr -d ' ')"
check "MAIN auth status mode" authenticated "$("${CURL[@]}" -H "Authorization: Bearer $ADMIN" "$BASE/v1/auth/status" | jq -r .mode)"

note
note "== phase 4: the minted key restores access, enrollment included =="
check "GET /v1/instances with key" 200 "$("${CURL[@]}" -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $ADMIN" "$BASE/v1/instances")"
check "POST /v1/templates with key" 201 "$("${CURL[@]}" -o /dev/null -w '%{http_code}' -X POST -H 'content-type: application/json' -H "Authorization: Bearer $ADMIN" \
  -d '{"tag":"anon-bootstrap-2","spec":{"name":"anon-bootstrap-2","version":"1","nodes":[{"type":"worker","executor":"http-node"}]}}' "$BASE/v1/templates")"
MKEY=$("$CLI" auth init --endpoint "https://127.0.0.1:$MPORT" 2>&1 | sed -n 's/.*RIMSKY_API_KEY="\(.*\)".*/\1/p')
if [ -z "$MKEY" ]; then
  MKEY=$("${MCURL[@]}" -X POST -H 'content-type: application/json' -d '{"name":"admin","permissions":[{"action":"*"}]}' "$MBASE/v1/auth/keys" | jq -r .plaintext)
fi
check "MTLS first admin key minted" yes "$([ -n "$MKEY" ] && [ "$MKEY" != null ] && echo yes || echo no)"
check "MTLS POST /v1/enroll with key" 200 "$("${MCURL[@]}" -o /dev/null -w '%{http_code}' -X POST -H 'content-type: application/json' -H "Authorization: Bearer $MKEY" -d '{"label":"exp"}' "$MBASE/v1/enroll")"
check "MTLS GET /v1/ca-root still unauthenticated after lockdown" 200 "$("${MCURL[@]}" -o /dev/null -w '%{http_code}' "$MBASE/v1/ca-root")"

note
if [ "$fail" = 0 ]; then note "EXPERIMENT PASS"; else note "EXPERIMENT FAIL"; fi
exit "$fail"
