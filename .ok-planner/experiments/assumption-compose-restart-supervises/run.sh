#!/bin/bash
# Experiment: assumption compose-restart-supervises.
#
# Declares a compose instance with `restart: always`, brings the project up,
# force-terminates the instance, and then watches -- through the public
# surface only -- for anything to re-create it. Nothing is run in between: if
# a compose runtime supervises the instance, it has an idle deployment and a
# terminal instance to act on. The run then invokes `compose up` by hand to
# establish that the policy is real, and repeats the whole shape for
# `restart: never` as the control.
#
# Requires: docker, python3, a rimsky CLI binary (RIMSKY_BIN), RIMSKY_IMAGE_TAG.
set -u

RIMSKY_BIN=${RIMSKY_BIN:?set RIMSKY_BIN to the rimsky CLI binary}
TAG=${RIMSKY_IMAGE_TAG:?set RIMSKY_IMAGE_TAG}
free_port() { python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()'; }
PORT=${PORT:-$(free_port)}
NAME=exp-assumption-compose-restart-supervises
BASE="http://127.0.0.1:$PORT"

WORK=$(mktemp -d); export HOME="$WORK/home"; mkdir -p "$HOME"
unset RIMSKY_API_KEY RIMSKY_CONTEXT
cleanup() { docker rm -f "$NAME" >/dev/null 2>&1; }
trap cleanup EXIT

fail=0
pass() { echo "PASS  $1"; }
bad()  { echo "FAIL  $1"; fail=1; }

docker rm -f "$NAME" >/dev/null 2>&1
docker run -d --name "$NAME" -p "$PORT:8080" "rimsky-all-in-one:$TAG" >/dev/null || exit 1
for _ in $(seq 1 90); do
  [ "$(curl -s -m 2 -o /dev/null -w '%{http_code}' "$BASE/v1/health")" = 200 ] && break; sleep 1
done
[ "$(curl -s -m 2 -o /dev/null -w '%{http_code}' "$BASE/v1/health")" = 200 ] || { echo "FAIL  stack did not come up"; exit 1; }
export RIMSKY_CONTROL_API_URL="$BASE"

live() { "$RIMSKY_BIN" instance list -o json 2>/dev/null | python3 -c '
import json,sys
try:
    rows = json.load(sys.stdin) or []
except Exception:
    print(-1); raise SystemExit
print(sum(1 for i in rows if not i.get("terminated_at")))'; }

probe() { # probe <policy>
  local policy=$1
  local proj="$WORK/$policy"
  mkdir -p "$proj"
  printf 'name: restart-probe-%s\nversion: "1"\nnodes:\n  - type: a\n    executor: verifier-shape-checks\n' "$policy" > "$proj/t.yml"
  printf 'project: restart-%s\ntemplates:\n  - path: t.yml\n    tag: probe\n    state: deployed\ninstances:\n  - template: probe\n    name: one\n    restart: %s\n' "$policy" "$policy" > "$proj/rimsky-compose.yml"
  ( cd "$proj" && "$RIMSKY_BIN" compose up --yes ) >/dev/null 2>&1
  local before; before=$(live)
  local id; id=$("$RIMSKY_BIN" instance list -o json 2>/dev/null | python3 -c '
import json,sys
try:
    rows=[i for i in (json.load(sys.stdin) or []) if not i.get("terminated_at")]
except Exception:
    rows=[]
print(rows[-1]["id"] if rows else "")')
  [ -n "$id" ] || { bad "restart: $policy — compose up created no live instance"; return; }
  "$RIMSKY_BIN" instance kill "$id" --force >/dev/null 2>&1
  echo "  -- restart: $policy — instance ${id:0:8} terminated; watching for a re-creation"
  local recreated=no
  for i in $(seq 1 30); do
    if [ "$(live)" -ge "$before" ]; then recreated=yes; break; fi
    sleep 1
  done
  local st; st=$( cd "$proj" && "$RIMSKY_BIN" compose status 2>&1 | grep instance | head -1 )
  echo "     after 30 polls: live instances $(live) (was $before); compose status says: $st"
  if [ $recreated = yes ]; then
    pass "restart: $policy — something re-created the instance without being asked"
  else
    bad "restart: $policy — nothing re-created it; the policy is inert until the next compose up"
  fi
  local plan; plan=$( cd "$proj" && "$RIMSKY_BIN" compose plan 2>&1 | grep -E '^\s+[+-] ' | sed 's/^[[:space:]]*//' | tr '\n' ';' )
  ( cd "$proj" && "$RIMSKY_BIN" compose up --yes ) >/dev/null 2>&1
  echo "     a hand-run compose plan then says: ${plan:-no changes}"
  echo "     live instances after that compose up: $(live)"
}

echo "== the policy the prior is about =="
probe always
echo
echo "== the control =="
probe never

echo
if [ $fail -eq 0 ]; then echo "RESULT: PASS"; else echo "RESULT: FAIL"; fi
exit $fail
