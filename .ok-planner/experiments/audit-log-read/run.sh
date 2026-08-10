#!/usr/bin/env bash
# Experiment: audit-log-read
#
# Provokes each auth-relevant action the story names against a fresh stack, then
# reads them back through GET /v1/audit and exercises every filter the surface
# offers:
#   key creates / revokes / rotates      -> the three key-lifecycle records
#   dry-run-mode access attempts         -> a write driven with ?dry_run=true
#   denied attempts                      -> a permission denial and a no-token denial
#   filtering                            -> kind, key_name, action, action_prefix,
#                                           target, status, mode, since, limit+cursor

set -u
cd "$(dirname "$0")"
ROOT=$(cd ../../.. && pwd)
: "${RIMSKY_IMAGE_TAG:?export RIMSKY_IMAGE_TAG=src-<tree hash> first}"
CLI="$ROOT/bin/rimsky"
PORT=${PORT:-18501}
NAME=exp-audit-log-read
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

req() { local k=$1 m=$2 p=$3 b=${4:-}
  curl -sS -m 10 -w '\n%{http_code}' -X "$m" -H 'content-type: application/json' \
    ${k:+-H "Authorization: Bearer $k"} ${b:+-d "$b"} "$BASE$p"; }
code() { req "$@" | tail -1; }
body() { req "$@" | sed '$d'; }
spec() { printf '{"tag":"%s","spec":{"name":"%s","version":"1","nodes":[{"type":"worker","executor":"http-node"}]}}' "$1" "$1"; }
audit() { body "$ADMIN" GET "/v1/audit$1"; }

docker rm -f "$NAME" >/dev/null 2>&1
docker run -d --name "$NAME" -p "$PORT:8080" "rimsky-all-in-one:$RIMSKY_IMAGE_TAG" >/dev/null || exit 1
until [ "$(code '' GET /v1/health)" = 200 ]; do sleep 0.5; done

note "== provoke every auth-relevant action the story names =="
ADMIN=$("$CLI" auth init --endpoint "$BASE" | sed -n 's/.*RIMSKY_API_KEY="\(.*\)".*/\1/p')
READER=$("$CLI" auth create-key --endpoint "$BASE" --key "$ADMIN" --name reader --role read-only | sed -n 's/.*RIMSKY_API_KEY="\(.*\)".*/\1/p')
DOOMED=$("$CLI" auth create-key --endpoint "$BASE" --key "$ADMIN" --name doomed --role read-only | sed -n 's/.*RIMSKY_API_KEY="\(.*\)".*/\1/p')
ROTATED=$("$CLI" auth create-key --endpoint "$BASE" --key "$ADMIN" --name rotated --role read-only | sed -n 's/.*RIMSKY_API_KEY="\(.*\)".*/\1/p')
"$CLI" auth revoke doomed --endpoint "$BASE" --key "$ADMIN" >/dev/null
"$CLI" auth rotate rotated --endpoint "$BASE" --key "$ADMIN" --grace 24h >/dev/null
SPEC=$(spec audit-probe)
check "an executed write"          201 "$(code "$ADMIN" POST /v1/templates "$SPEC")"
check "a dry-run write"            200 "$(code "$ADMIN" POST "/v1/templates?dry_run=true" "$SPEC")"
check "a permission denial"        403 "$(code "$READER" POST /v1/templates "$SPEC")"
check "a no-token denial"          401 "$(code '' GET /v1/instances)"
check "an invalid-token denial"    401 "$(code 'rk_notarealkeynotarealkeynotarealkey1234' GET /v1/instances)"

note
note "== the audit log carries every record kind =="
ALL=$(audit "?limit=500")
note "kinds observed:"
printf '%s' "$ALL" | jq -r '[.audit[].kind]|group_by(.)|map({(.[0]):length})|add' | sed 's/^/    /'
for k in auth.key_created auth.key_revoked auth.key_rotated auth.access_attempted auth.access_denied; do
  check "kind present: $k" yes "$(printf '%s' "$ALL" | jq -r --arg k "$k" 'if ([.audit[].kind]|index($k)) then "yes" else "no" end')"
done

note
note "== the records carry what an operator needs =="
check "key_created names each minted key" "admin doomed reader rotated" \
  "$(printf '%s' "$ALL" | jq -r '[.audit[]|select(.kind=="auth.key_created").payload.key_name]|sort|join(" ")')"
check "key_revoked names the revoked key" doomed \
  "$(printf '%s' "$ALL" | jq -r '[.audit[]|select(.kind=="auth.key_revoked").payload.key_name]|join(" ")')"
check "key_rotated names the rotated key" rotated \
  "$(printf '%s' "$ALL" | jq -r '[.audit[]|select(.kind=="auth.key_rotated").payload.key_name]|join(" ")')"
check "the dry-run write was recorded as dry_run and not executed" "dry_run false" \
  "$(printf '%s' "$ALL" | jq -r '[.audit[]|select(.kind=="auth.access_attempted" and .payload.mode=="dry_run")][0]|"\(.payload.mode) \(.payload.executed)"')"
check "the executed write was recorded as executed" "execute true" \
  "$(printf '%s' "$ALL" | jq -r '[.audit[]|select(.kind=="auth.access_attempted" and .payload.action=="template:register" and .payload.mode=="execute")][0]|"\(.payload.mode) \(.payload.executed)"')"
check "denial reasons are recorded" "invalid_token no_token permission_denied" \
  "$(printf '%s' "$ALL" | jq -r '[.audit[]|select(.kind=="auth.access_denied").payload.denial_reason]|unique|join(" ")' | tr '\n' ' ' | sed 's/ $//')"

note
note "== filtering =="
check "?kind=auth.key_created" 4 "$(audit "?kind=auth.key_created" | jq '.audit|length')"
check "?kind rejects a non-audit kind" 400 "$(code "$ADMIN" GET "/v1/audit?kind=state_transition")"
check "?key_name=reader returns only that key's records" reader \
  "$(audit "?key_name=reader&limit=500" | jq -r '[.audit[].payload.key_name]|unique|join(" ")')"
check "?action=template:register returns only that action" "template:register" \
  "$(audit "?action=template:register&limit=500" | jq -r '[.audit[].payload.action]|unique|join(" ")')"
check "?action_prefix=auth: returns only auth actions" yes \
  "$(audit "?action_prefix=auth:&limit=500" | jq -r 'if ([.audit[].payload.action]|unique|map(startswith("auth:"))|all) then "yes" else "no" end')"
check "?target=/v1/templates returns only that path" "/v1/templates" \
  "$(audit "?target=/v1/templates&limit=500" | jq -r '[.audit[].payload.request_path]|unique|join(" ")')"
check "?status=403 returns only denials with that status" 403 \
  "$(audit "?status=403&limit=500" | jq -r '[.audit[].payload.response_status]|unique|join(" ")')"
check "?mode=dry_run returns only dry-run attempts" dry_run \
  "$(audit "?mode=dry_run&limit=500" | jq -r '[.audit[].payload.mode]|unique|join(" ")')"
SINCE=$(date -u -v+1M '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null || date -u -d '+1 minute' '+%Y-%m-%dT%H:%M:%SZ')
check "?since in the future returns nothing" 0 "$(audit "?since=$SINCE" | jq '.audit|length')"
check "?since rejects a non-RFC3339 value" 400 "$(code "$ADMIN" GET "/v1/audit?since=yesterday")"
FIRST=$(audit "?limit=1")
check "?limit=1 returns one record and a cursor" "1 yes" \
  "$(printf '%s' "$FIRST" | jq -r '"\(.audit|length) \(if .next_cursor != "" then "yes" else "no" end)"')"
CUR=$(printf '%s' "$FIRST" | jq -r .next_cursor)
check "the cursor pages forward to a different record" yes \
  "$(printf '%s' "$(audit "?limit=1&cursor=$CUR")" | jq -r --arg a "$(printf '%s' "$FIRST" | jq -r '.audit[0].id')" 'if .audit[0].id != $a then "yes" else "no" end')"

note
note "== reading the audit log is itself a gated action =="
check "read-only role may read the audit log" 200 "$(code "$READER" GET /v1/audit)"
NOAUDIT=$("$CLI" auth create-key --endpoint "$BASE" --key "$ADMIN" --name noaudit --role publisher-service | sed -n 's/.*RIMSKY_API_KEY="\(.*\)".*/\1/p')
check "a key without audit:read is refused" 403 "$(code "$NOAUDIT" GET /v1/audit)"

note
if [ "$fail" = 0 ]; then note "EXPERIMENT PASS"; else note "EXPERIMENT FAIL"; fi
exit "$fail"
