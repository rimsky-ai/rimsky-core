#!/bin/bash
# Experiment: assumption cli-dry-run-flag-exists.
#
# Two questions:
#   1. Does any CLI write verb take --dry-run (or a --preview / -n spelling)?
#      The parser answers without a server: the endpoint points at a closed
#      port, so "connection refused" means the flag was accepted and "flag
#      provided but not defined" means it was not. The population is every
#      write verb in `rimsky --help`.
#   2. Is the platform's dry-run reachable at all from the outside? The run
#      boots a rimsky-all-in-one, registers a template through the CLI, and
#      asks the same deploy the CLI verb performs for a preview over the HTTP
#      route, so the CLI's silence is measured against a working capability
#      rather than against nothing.
#
# Requires: docker, python3, a rimsky CLI binary (RIMSKY_BIN), RIMSKY_IMAGE_TAG.
set -u

RIMSKY_BIN=${RIMSKY_BIN:?set RIMSKY_BIN to the rimsky CLI binary}
TAG=${RIMSKY_IMAGE_TAG:?set RIMSKY_IMAGE_TAG}
free_port() { python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()'; }
PORT=${PORT:-$(free_port)}
NAME=exp-assumption-cli-dry-run
BASE="http://127.0.0.1:$PORT"

WORK=$(mktemp -d)
export HOME="$WORK/home"; mkdir -p "$HOME"
unset RIMSKY_API_KEY RIMSKY_CONTEXT
cleanup() { docker rm -f "$NAME" >/dev/null 2>&1; }
trap cleanup EXIT

fail=0
pass() { echo "PASS  $1"; }
bad()  { echo "FAIL  $1"; fail=1; }

WRITE_VERBS=(
  "register f.yml" "deploy t" "undeploy t" "instantiate t" "rm-instance i" "run f.yml"
  "template register f.yml" "template deploy t" "template undeploy t" "template rm t"
  "tag create x --template t" "tag mv x" "tag rm x"
  "instance create t" "instance delete i" "instance kill i"
  "admin reset" "lineage prune" "asset delete --instance i a"
  "auth init" "auth create-key --name n --role admin" "auth revoke k" "auth rotate k"
  "ctx add c --endpoint http://127.0.0.1:1" "ctx rm c" "ctx use c"
  "compose up" "compose down" "compose run"
  "agent start" "agent stop"
)
# The compose family stops parsing at its first positional, so its probe puts
# the flag before the manifest; every other verb hoists trailing flags.
positional_after() { case "$1" in "compose run") printf 'm.yml' ;; *) printf '' ;; esac; }

echo "== stage 1: does any write verb take a dry-run flag? (${#WRITE_VERBS[@]} verbs) =="
export RIMSKY_CONTROL_API_URL=http://127.0.0.1:1
for spelling in --dry-run --preview -n; do
  accepted=0; names=""
  for v in "${WRITE_VERBS[@]}"; do
    read -r -a words <<<"$v"
    tail_arg=$(positional_after "$v")
    if [ -n "$tail_arg" ]; then
      out=$("$RIMSKY_BIN" "${words[@]}" "$spelling" "$tail_arg" 2>&1)
    else
      out=$("$RIMSKY_BIN" "${words[@]}" "$spelling" 2>&1)
    fi
    case "$out" in
      *"flag provided but not defined: ${spelling#--}"*|*"flag provided but not defined: ${spelling#-}"*) ;;
      *) accepted=$((accepted+1)); names="$names $v" ;;
    esac
  done
  echo "     $spelling : accepted by $accepted of ${#WRITE_VERBS[@]} write verbs$names"
  if [ "$spelling" = "--dry-run" ]; then
    if [ $accepted -eq ${#WRITE_VERBS[@]} ]; then
      pass "--dry-run parses on every write verb"
    else
      bad "--dry-run parses on $accepted of ${#WRITE_VERBS[@]} write verbs"
    fi
  fi
done
unset RIMSKY_CONTROL_API_URL

echo
echo "== stage 2: is the preview reachable from outside at all? =="
docker rm -f "$NAME" >/dev/null 2>&1
docker run -d --name "$NAME" -p "$PORT:8080" "rimsky-all-in-one:$TAG" >/dev/null || exit 1
for _ in $(seq 1 90); do
  [ "$(curl -s -m 2 -o /dev/null -w '%{http_code}' "$BASE/v1/health")" = 200 ] && break; sleep 1
done
[ "$(curl -s -m 2 -o /dev/null -w '%{http_code}' "$BASE/v1/health")" = 200 ] || { echo "FAIL  stack did not come up"; exit 1; }
export RIMSKY_CONTROL_API_URL="$BASE"

cat > "$WORK/t.yml" <<'EOF'
name: dry-run-flag-probe
version: "1"
nodes:
  - type: verify
    executor: verifier-shape-checks
EOF
H=$("$RIMSKY_BIN" template register "$WORK/t.yml" -o json | python3 -c 'import json,sys;print(json.load(sys.stdin)["template_id"])')
[ -n "$H" ] || { echo "FAIL  could not register the probe template"; exit 1; }

body=$(curl -s -X POST "$BASE/v1/templates/$H/deploy?dry_run=true")
echo "     POST /v1/templates/{id}/deploy?dry_run=true → $body"
if printf '%s' "$body" | grep -q '"dry_run":true'; then
  echo "     the platform does preview this deploy; the question is only whether the CLI can ask"
else
  echo "     the platform did not preview this deploy either"
fi
state=$("$RIMSKY_BIN" template get "$H" -o json | python3 -c 'import json,sys;print(json.load(sys.stdin).get("state"))')
echo "     template state after the preview: $state"

out=$("$RIMSKY_BIN" deploy "$H" --dry-run 2>&1); rc=$?
echo "     rimsky deploy <hash> --dry-run → exit $rc: $(printf '%s' "$out" | head -1)"
if [ $rc -eq 0 ] && printf '%s' "$out" | grep -qi 'dry'; then
  pass "the CLI can ask for the same preview"
else
  bad "the CLI cannot ask for the preview the HTTP route serves"
fi
state2=$("$RIMSKY_BIN" template get "$H" -o json | python3 -c 'import json,sys;print(json.load(sys.stdin).get("state"))')
echo "     template state after the CLI attempt: $state2"

echo
echo "== stage 3: the route the CLI does leave open — a dry-run-mode key =="
ADMIN=$("$RIMSKY_BIN" auth init | awk 'NF==1 && length($1)>20 {print $1; exit}')
if [ -z "$ADMIN" ]; then
  echo "     could not mint an admin key; skipping"
else
  cat > "$WORK/role.json" <<'EOF'
{
  "name": "preview-only",
  "description": "Every template write pinned to dry-run.",
  "permissions": [
    { "action": "template:deploy", "mode": "dry_run" },
    { "action": "template:read" }
  ]
}
EOF
  PREVIEW=$("$RIMSKY_BIN" auth create-key --name=preview-only --role-file="$WORK/role.json" \
    --key "$ADMIN" | awk 'NF==1 && length($1)>20 {print $1; exit}')
  out=$("$RIMSKY_BIN" deploy "$H" --key "$PREVIEW" 2>&1); rc=$?
  echo "     rimsky deploy <hash> with a dry-run-mode key → exit $rc: $(printf '%s' "$out" | head -3 | tr '\n' ' ')"
  outj=$("$RIMSKY_BIN" deploy "$H" --key "$PREVIEW" -o json 2>&1)
  echo "     the same, -o json → $(printf '%s' "$outj" | tr '\n' ' ')"
  if printf '%s' "$out" | grep -qi 'dry'; then
    echo "     the human rendering says the write was previewed"
  else
    echo "     the human rendering reports the write as done, with no sign it was a preview"
  fi
  state3=$("$RIMSKY_BIN" template get "$H" -o json --key "$ADMIN" | python3 -c 'import json,sys;print(json.load(sys.stdin).get("state"))')
  echo "     template state afterwards: $state3"
  if [ "$state3" = "registered" ]; then
    echo "     the preview is reachable from the CLI only by minting a key that can never deploy for real"
  fi
fi

echo
if [ $fail -eq 0 ]; then echo "RESULT: PASS"; else echo "RESULT: FAIL"; fi
exit $fail
