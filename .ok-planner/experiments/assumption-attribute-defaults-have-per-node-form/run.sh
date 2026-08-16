#!/bin/bash
# Experiment: assumption attribute-defaults-have-per-node-form.
#
# Asks a live deployment which selectors each of the two attribute-defaults
# surfaces takes. At the template level the run registers one template per
# candidate selector under `defaults.attributes` -- by_executor, by_node,
# by_match -- and records which are accepted. At the instance level it creates
# one instance per selector under `attribute_overrides` over the HTTP route,
# which is where the CLI has no flag. The two answers side by side are the
# measurement.
#
# Requires: docker, python3, a rimsky CLI binary (RIMSKY_BIN), RIMSKY_IMAGE_TAG.
set -u

RIMSKY_BIN=${RIMSKY_BIN:?set RIMSKY_BIN to the rimsky CLI binary}
TAG=${RIMSKY_IMAGE_TAG:?set RIMSKY_IMAGE_TAG}
free_port() { python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()'; }
PORT=${PORT:-$(free_port)}
NAME=exp-assumption-attribute-defaults-have-per-node-form
BASE="http://127.0.0.1:$PORT"

WORK=$(mktemp -d); export HOME="$WORK/home"; mkdir -p "$HOME"
unset RIMSKY_API_KEY RIMSKY_CONTEXT
cleanup() { docker rm -f "$NAME" >/dev/null 2>&1; }
trap cleanup EXIT

fail=0

docker rm -f "$NAME" >/dev/null 2>&1
docker run -d --name "$NAME" -p "$PORT:8080" "rimsky-all-in-one:$TAG" >/dev/null || exit 1
for _ in $(seq 1 90); do
  [ "$(curl -s -m 2 -o /dev/null -w '%{http_code}' "$BASE/v1/health")" = 200 ] && break; sleep 1
done
[ "$(curl -s -m 2 -o /dev/null -w '%{http_code}' "$BASE/v1/health")" = 200 ] || { echo "FAIL  stack did not come up"; exit 1; }
export RIMSKY_CONTROL_API_URL="$BASE"
ADMIN=$("$RIMSKY_BIN" auth init | awk 'NF==1 && length($1)>20 {print $1; exit}')
[ -n "$ADMIN" ] || { echo "FAIL  could not mint the admin key"; exit 1; }
export RIMSKY_API_KEY="$ADMIN"

echo "== template level: defaults.attributes.<selector> =="
tpl_accepted=""
for sel in by_executor by_node by_match; do
  case $sel in
    by_executor) frag='      verifier-shape-checks:\n        checks: []' ;;
    by_node)     frag='      a:\n        checks: []' ;;
    by_match)    frag='      - when:\n          checks: []\n        set:\n          checks: []' ;;
  esac
  {
    printf 'name: attr-defaults-%s\nversion: "1"\ndefaults:\n  attributes:\n    %s:\n' "$sel" "$sel"
    printf "$frag\n"
    printf 'nodes:\n  - type: a\n    executor: verifier-shape-checks\n'
  } > "$WORK/$sel.yml"
  out=$("$RIMSKY_BIN" template register "$WORK/$sel.yml" 2>&1 | head -2 | tr '\n' ' ')
  if printf '%s' "$out" | grep -q 'template_hash'; then
    tpl_accepted="$tpl_accepted $sel"
    printf '  defaults.attributes.%-12s accepted\n' "$sel"
  else
    printf '  defaults.attributes.%-12s rejected — %s\n' "$sel" "$(printf '%s' "$out" | cut -c1-70)"
  fi
done

echo
echo "== instance level: attribute_overrides.<selector> =="
cat > "$WORK/base.yml" <<'EOF'
name: attr-defaults-base
version: "1"
nodes:
  - type: a
    executor: verifier-shape-checks
EOF
H=$("$RIMSKY_BIN" template register "$WORK/base.yml" -o json | python3 -c 'import json,sys;print(json.load(sys.stdin)["template_id"])')
"$RIMSKY_BIN" template deploy "$H" >/dev/null
inst_accepted=""
for sel in by_executor by_node by_match; do
  case $sel in
    by_executor) frag='{"verifier-shape-checks":{"checks":[]}}' ;;
    by_node)     frag='{"a":{"checks":[]}}' ;;
    by_match)    frag='[{"matcher":{"node_type":"a"},"overlay":{"checks":[]}}]' ;;
  esac
  code=$(curl -s -o "$WORK/resp" -w '%{http_code}' -X POST "$BASE/v1/instances" \
    -H 'Content-Type: application/json' -H "Authorization: Bearer $ADMIN" \
    -d "{\"template\":\"$H\",\"attribute_overrides\":{\"$sel\":$frag}}")
  if [ "$code" = 201 ] || [ "$code" = 200 ]; then
    inst_accepted="$inst_accepted $sel"
    printf '  attribute_overrides.%-12s accepted (HTTP %s)\n' "$sel" "$code"
  else
    printf '  attribute_overrides.%-12s rejected (HTTP %s) — %s\n' "$sel" "$code" "$(head -c 90 "$WORK/resp")"
  fi
done

echo
echo "  template level accepts:$tpl_accepted"
echo "  instance level accepts:$inst_accepted"
if printf '%s' "$tpl_accepted" | grep -q by_node; then
  echo "PASS  a per-node defaults form exists on the template"
else
  echo "FAIL  the template takes only$tpl_accepted while the instance takes$inst_accepted"
  fail=1
fi

echo
if [ $fail -eq 0 ]; then echo "RESULT: PASS"; else echo "RESULT: FAIL"; fi
exit $fail
