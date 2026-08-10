#!/usr/bin/env bash
# Experiment: grant-scope-enforcement
#
# An operator delegates control-plane access to a per-tenant key whose grant is
# scoped to one template-tag ("alpha"). Every scopeable action is then driven
# twice from that key — once against the in-scope tag, once against an
# out-of-scope tag ("beta") owned by the admin — across the whole template
# lifecycle: register, deploy, tag move, tag delete, instance create, undeploy,
# deregister. The in-scope call must succeed and the out-of-scope call must be
# refused with 403.
#
# The scoped grant is delivered the way an operator delivers one:
# `rimsky auth create-key --role-file`.

set -u
cd "$(dirname "$0")"
ROOT=$(cd ../../.. && pwd)
: "${RIMSKY_IMAGE_TAG:?export RIMSKY_IMAGE_TAG=src-<tree hash> first}"
CLI="$ROOT/bin/rimsky"
PORT=${PORT:-18481}
NAME=exp-grant-scope
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

req() { # req <key> <method> <path> [body] -> "<status> <body>"
  local k=$1 m=$2 p=$3 b=${4:-}
  curl -sS -m 10 -w '\n%{http_code}' -X "$m" -H 'content-type: application/json' \
    ${k:+-H "Authorization: Bearer $k"} ${b:+-d "$b"} "$BASE$p"
}
code() { req "$@" | tail -1; }
body() { req "$@" | sed '$d'; }
# Bodies are built by helpers rather than written inline: a literal {a,b} inside a
# nested command substitution is brace-expanded by the shell before curl sees it.
spec() { printf '{"tag":"%s","spec":{"name":"%s","version":"1","nodes":[{"type":"worker","executor":"http-node"}]}}' "$1" "$1"; }
tagbody() { printf '{"tag":"%s","template":"%s"}' "$1" "$2"; }
movebody() { printf '{"template":"%s"}' "$1"; }
instbody() { printf '{"template":"%s","instance_key":"%s","params":{},"target_agent":"tenant"}' "$1" "$2"; }
keybody() { printf '{"name":"probe","permissions":[{"action":"*"}]}'; }

docker rm -f "$NAME" >/dev/null 2>&1
docker run -d --name "$NAME" -p "$PORT:8080" "rimsky-all-in-one:$RIMSKY_IMAGE_TAG" >/dev/null || exit 1
until [ "$(code '' GET /v1/health)" = 200 ]; do sleep 0.5; done

ADMIN=$("$CLI" auth init --endpoint "$BASE" | sed -n 's/.*RIMSKY_API_KEY="\(.*\)".*/\1/p')

cat > "$WORK/tenant-alpha.json" <<'JSON'
{
  "name": "tenant-alpha",
  "description": "Least-privilege delegation: the full template lifecycle, but only for the alpha tag.",
  "permissions": [
    {"action": "*:read"},
    {"action": "template:register",   "scope": {"template_tag": "alpha"}},
    {"action": "template:deploy",     "scope": {"template_tag": "alpha"}},
    {"action": "template:undeploy",   "scope": {"template_tag": "alpha"}},
    {"action": "template:deregister", "scope": {"template_tag": "alpha"}},
    {"action": "tag:set",             "scope": {"template_tag": "alpha"}},
    {"action": "tag:delete",          "scope": {"template_tag": "alpha"}},
    {"action": "instance:create",     "scope": {"template_tag": "alpha"}}
  ]
}
JSON
TENANT=$("$CLI" auth create-key --endpoint "$BASE" --key "$ADMIN" --name tenant-alpha \
  --role-file "$WORK/tenant-alpha.json" | sed -n 's/.*RIMSKY_API_KEY="\(.*\)".*/\1/p')
check "scoped key minted" yes "$([ -n "$TENANT" ] && echo yes || echo no)"
check "the scoped grant reads back off the key" \
  "alpha alpha alpha alpha alpha alpha alpha" \
  "$("$CLI" auth show tenant-alpha --endpoint "$BASE" --key "$ADMIN" | jq -r '[.permissions[].scope.template_tag|select(.)]|join(" ")')"

note
note "== register =="
check "tenant registers into its own tag" 201 "$(code "$TENANT" POST /v1/templates "$(spec alpha)")"
check "tenant refused registering into another tag" 403 "$(code "$TENANT" POST /v1/templates "$(spec beta)")"
TA=$(body "$ADMIN" GET "/v1/tags" | jq -r '.tags[]|select(.tag=="alpha").template_id')
check "alpha tag resolves to the tenant's template" yes "$([ -n "$TA" ] && echo yes || echo no)"
TB=$(body "$ADMIN" POST /v1/templates "$(spec beta)" | jq -r .template_id)
check "admin registers the beta template" yes "$([ -n "$TB" ] && [ "$TB" != null ] && echo yes || echo no)"

note
note "== deploy =="
check "tenant deploys its own template" 200 "$(code "$TENANT" POST "/v1/templates/$TA/deploy" '{}')"
check "tenant refused deploying the beta template" 403 "$(code "$TENANT" POST "/v1/templates/$TB/deploy" '{}')"
check "admin deploys the beta template" 200 "$(code "$ADMIN" POST "/v1/templates/$TB/deploy" '{}')"

note
note "== tag move and tag delete =="
MOVE_A=$(movebody "$TA"); MOVE_B=$(movebody "$TB"); TAG_A=$(tagbody alpha "$TA")
check "tenant moves its own tag" 200 "$(code "$TENANT" PUT /v1/tags/alpha "$MOVE_A")"
check "tenant refused moving the beta tag" 403 "$(code "$TENANT" PUT /v1/tags/beta "$MOVE_B")"
check "tenant refused deleting the beta tag" 403 "$(code "$TENANT" DELETE /v1/tags/beta)"
check "tenant deletes its own tag" 200 "$(code "$TENANT" DELETE /v1/tags/alpha)"
check "admin restores the alpha tag" 201 "$(code "$ADMIN" POST /v1/tags "$TAG_A")"

note
note "== instance create =="
I1=$(body "$TENANT" POST /v1/instances "$(instbody alpha ck-alpha-1)" | jq -r '.instance_id // empty')
check "tenant creates an instance of its own tag" yes "$([ -n "$I1" ] && echo yes || echo no)"
BETA_TAG_BODY=$(instbody beta ck-beta-1); BETA_HASH_BODY=$(instbody "$TB" ck-beta-2)
check "tenant refused creating an instance of the beta tag" 403 \
  "$(code "$TENANT" POST /v1/instances "$BETA_TAG_BODY")"
check "tenant refused creating an instance by the beta template hash" 403 \
  "$(code "$TENANT" POST /v1/instances "$BETA_HASH_BODY")"
I2=$(body "$TENANT" POST /v1/instances "$(instbody "$TA" ck-alpha-2)" | jq -r '.instance_id // empty')
check "tenant creates an instance by its own template hash" yes "$([ -n "$I2" ] && echo yes || echo no)"

for id in $I1 $I2; do
  code "$ADMIN" POST "/v1/instances/$id/terminate" '{}' >/dev/null
  code "$ADMIN" DELETE "/v1/instances/$id" >/dev/null
done

note
note "== undeploy and deregister =="
check "tenant undeploys its own template" 200 "$(code "$TENANT" POST "/v1/templates/$TA/undeploy" '{}')"
check "tenant refused undeploying the beta template" 403 "$(code "$TENANT" POST "/v1/templates/$TB/undeploy" '{}')"
check "tenant refused deregistering the beta template" 403 "$(code "$TENANT" DELETE "/v1/templates/$TB")"
check "tenant deregisters its own template" 200 "$(code "$TENANT" DELETE "/v1/templates/$TA")"
check "the beta template survived every out-of-scope attempt" 200 "$(code "$ADMIN" GET "/v1/templates/$TB")"

note
note "== the scoped key is still a real key for unscoped reads =="
check "tenant may read the template list" 200 "$(code "$TENANT" GET /v1/templates)"
KEYBODY=$(keybody)
check "tenant may not mint keys" 403 "$(code "$TENANT" POST /v1/auth/keys "$KEYBODY")"

note
if [ "$fail" = 0 ]; then note "EXPERIMENT PASS"; else note "EXPERIMENT FAIL"; fi
exit "$fail"
