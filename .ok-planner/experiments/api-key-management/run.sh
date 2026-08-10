#!/usr/bin/env bash
# Experiment: api-key-management
#
# Drives the whole api-key lifecycle through the `rimsky auth` CLI verbs against a
# fresh `rimsky-all-in-one` stack, and checks each effect through the control API:
#   bootstrap  -> auth init on a fresh deployment
#   mint       -> auth create-key with a role, and the role actually binds
#   inspect    -> auth list / auth show never surface plaintext
#   revoke     -> auth revoke, and the revoked key stops being accepted
#   rotate     -> auth rotate hands out new plaintext now and keeps the old key
#                 usable through the grace window, then stops accepting it
#   status     -> auth status reports the current mode

set -u
cd "$(dirname "$0")"
ROOT=$(cd ../../.. && pwd)
: "${RIMSKY_IMAGE_TAG:?export RIMSKY_IMAGE_TAG=src-<tree hash> first}"
CLI="$ROOT/bin/rimsky"
PORT=${PORT:-18471}
NAME=exp-api-key-management
WORK=$(mktemp -d)
BASE="http://127.0.0.1:$PORT"
export HOME="$WORK"
unset RIMSKY_API_KEY RIMSKY_CONTROL_API_URL 2>/dev/null

fail=0
note() { printf '%s\n' "$*"; }
check() {
  if [ "$2" = "$3" ]; then printf 'PASS  %-62s %s\n' "$1" "$3"
  else printf 'FAIL  %-62s expected [%s] got [%s]\n' "$1" "$2" "$3"; fail=1; fi
}
cleanup() { docker rm -f "$NAME" >/dev/null 2>&1; rm -rf "$WORK"; }
trap cleanup EXIT

rk() { "$CLI" auth "$@" --endpoint "$BASE"; }
code() { # code <key> <method> <path> [body]
  local k=$1 m=$2 p=$3 b=${4:-}
  if [ -n "$b" ]; then
    curl -sS -m 10 -o /dev/null -w '%{http_code}' -X "$m" -H 'content-type: application/json' \
      ${k:+-H "Authorization: Bearer $k"} -d "$b" "$BASE$p"
  else
    curl -sS -m 10 -o /dev/null -w '%{http_code}' -X "$m" ${k:+-H "Authorization: Bearer $k"} "$BASE$p"
  fi
}
plaintext_of() { sed -n 's/.*RIMSKY_API_KEY="\{0,1\}\([^" ]*\)"\{0,1\} for subsequent.*/\1/p'; }

docker rm -f "$NAME" >/dev/null 2>&1
docker run -d --name "$NAME" -p "$PORT:8080" "rimsky-all-in-one:$RIMSKY_IMAGE_TAG" >/dev/null || exit 1
until [ "$(code '' GET /v1/health)" = 200 ]; do sleep 0.5; done

note "== bootstrap =="
check "auth status on a fresh deployment" "Mode: anonymous (0 keys provisioned)" "$(rk status)"
ADMIN=$(rk init | plaintext_of)
check "auth init surfaced an admin plaintext" yes "$([ -n "$ADMIN" ] && echo yes || echo no)"
check "auth status after init" "Mode: authenticated (1 keys total, 1 admin)" "$(rk status --key "$ADMIN")"
check "auth init refuses a second bootstrap" 1 "$(rk init --key "$ADMIN" >/dev/null 2>&1; echo $?)"

note
note "== mint a scoped key with a role =="
READER=$(rk create-key --key "$ADMIN" --name reader --role read-only | plaintext_of)
check "auth create-key surfaced a plaintext" yes "$([ -n "$READER" ] && echo yes || echo no)"
check "read-only key may read instances" 200 "$(code "$READER" GET /v1/instances)"
check "read-only key may not register a template" 403 \
  "$(code "$READER" POST /v1/templates '{"tag":"x","spec":{"name":"x","version":"1","nodes":[{"type":"worker","executor":"http-node"}]}}')"
EXPIRING=$(rk create-key --key "$ADMIN" --name shortlived --role read-only --expires 24h | plaintext_of)
check "auth create-key --expires minted a key" 200 "$(code "$EXPIRING" GET /v1/instances)"

note
note "== inspect without seeing plaintext =="
LIST=$(rk list --key "$ADMIN" --json)
SHOW=$(rk show reader --key "$ADMIN")
check "auth list names every key" "admin reader shortlived" "$(printf '%s' "$LIST" | jq -r '[.[].name]|sort|join(" ")')"
check "auth list carries no plaintext field" "" "$(printf '%s' "$LIST" | jq -r '[.[]|keys[]]|unique|map(select(test("plaintext")))|join(",")')"
check "auth list output does not contain a live plaintext" absent \
  "$(printf '%s' "$LIST" | grep -qF "$READER" && echo present || echo absent)"
check "auth show output does not contain a live plaintext" absent \
  "$(printf '%s' "$SHOW" | grep -qF "$READER" && echo present || echo absent)"
check "auth show reports the key by name" reader "$(printf '%s' "$SHOW" | jq -r .name)"
check "auth show reports the grant" '[{"action":"*:read"}]' "$(printf '%s' "$SHOW" | jq -c .permissions)"

note
note "== revoke =="
rk revoke shortlived --key "$ADMIN" >/dev/null
check "revoked key is no longer accepted" 401 "$(code "$EXPIRING" GET /v1/instances)"
check "revoked key drops out of the default listing" "admin reader" \
  "$(rk list --key "$ADMIN" --json | jq -r '[.[].name]|sort|join(" ")')"
check "revoked key is still inspectable with --include-revoked" "admin reader shortlived" \
  "$(rk list --key "$ADMIN" --json --include-revoked | jq -r '[.[].name]|sort|join(" ")')"

note
note "== rotate: new plaintext now, old key usable through the grace window =="
OLD=$(rk create-key --key "$ADMIN" --name rotating --role read-only | plaintext_of)
check "pre-rotation key works" 200 "$(code "$OLD" GET /v1/instances)"
ROT=$(rk rotate rotating --key "$ADMIN" --grace 5s)
printf '%s\n' "$ROT" | sed 's/^/    /'
NEW=$(printf '%s\n' "$ROT" | sed -n '/Save the new key plaintext now/{n;s/^ *//;p;}')
check "rotate surfaced a new plaintext" yes "$([ -n "$NEW" ] && [ "$NEW" != "$OLD" ] && echo yes || echo no)"
check "new key works immediately" 200 "$(code "$NEW" GET /v1/instances)"
check "old key still works inside the grace window" 200 "$(code "$OLD" GET /v1/instances)"
note "polling until the grace window closes on the old key (blocks until it does)"
until [ "$(code "$OLD" GET /v1/instances)" = 401 ]; do sleep 0.5; done
check "old key refused once the grace window has passed" 401 "$(code "$OLD" GET /v1/instances)"
check "new key still works after the grace window" 200 "$(code "$NEW" GET /v1/instances)"

note
note "== status =="
check "auth status reports the current mode and counts" "Mode: authenticated (3 keys total, 1 admin)" "$(rk status --key "$ADMIN")"

note
if [ "$fail" = 0 ]; then note "EXPERIMENT PASS"; else note "EXPERIMENT FAIL"; fi
exit "$fail"
