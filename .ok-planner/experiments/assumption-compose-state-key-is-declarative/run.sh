#!/bin/bash
# Experiment: assumption compose-state-key-is-declarative.
#
# Two claims, driven against one live deployment:
#   1. setting templates[].state to undeployed and re-running `compose up`
#      undeploys the template;
#   2. removing an entry from the manifest removes the resource.
#
# The run brings a project up with state: deployed, then flips the key to each
# other value the operator might write -- undeployed, then registered -- and
# re-runs up, reading the live template's state back after each. It then drops
# the instance entry while the instance is still live, drops it again once the
# instance is terminal, and finally drops the template entry.
#
# Requires: docker, python3, a rimsky CLI binary (RIMSKY_BIN), RIMSKY_IMAGE_TAG.
set -u

RIMSKY_BIN=${RIMSKY_BIN:?set RIMSKY_BIN to the rimsky CLI binary}
TAG=${RIMSKY_IMAGE_TAG:?set RIMSKY_IMAGE_TAG}
free_port() { python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()'; }
PORT=${PORT:-$(free_port)}
NAME=exp-assumption-compose-state-key-is-declarative
BASE="http://127.0.0.1:$PORT"

WORK=$(mktemp -d); export HOME="$WORK/home"; mkdir -p "$HOME"
PROJ="$WORK/proj"; mkdir -p "$PROJ"
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

printf 'name: state-key-probe\nversion: "1"\nnodes:\n  - type: a\n    executor: verifier-shape-checks\n' > "$PROJ/t.yml"
manifest() { # manifest <state> [with-instance]
  {
    printf 'project: state-key\ntemplates:\n  - path: t.yml\n    tag: probe\n    state: %s\n' "$1"
    [ "${2:-}" = with-instance ] && printf 'instances:\n  - template: probe\n    name: one\n'
  } > "$PROJ/rimsky-compose.yml"
}
tstate() { "$RIMSKY_BIN" template list -o json | python3 -c '
import json,sys
rows = json.load(sys.stdin) or []
print(rows[0]["state"] if rows else "gone")'; }
tcount() { "$RIMSKY_BIN" template list -o json | python3 -c 'import json,sys;print(len(json.load(sys.stdin) or []))'; }
icount() { "$RIMSKY_BIN" instance list -o json | python3 -c 'import json,sys;print(len(json.load(sys.stdin) or []))'; }

echo "== bring the project up with state: deployed =="
manifest deployed with-instance
( cd "$PROJ" && "$RIMSKY_BIN" compose up --yes ) 2>&1 | tail -1
echo "     template state: $(tstate)"

echo
echo "== claim 1: flip the state key =="
for want in undeployed registered; do
  manifest "$want" with-instance
  out=$( cd "$PROJ" && "$RIMSKY_BIN" compose up --yes 2>&1 | head -2 | tr '\n' ' ' )
  now=$(tstate)
  printf '     state: %-11s → compose up said: %-46s live state now: %s\n' "$want" "$(printf '%s' "$out" | cut -c1-46)" "$now"
  if [ "$want" = undeployed ]; then
    if [ "$now" = undeployed ]; then
      pass "state: undeployed undeploys the template"
    else
      bad "state: undeployed did not undeploy the template (state is $now)"
    fi
  else
    if [ "$now" = registered ]; then
      pass "state: registered rolls the template back to registered"
    else
      bad "state: registered left the template at $now — the key only moves forward"
    fi
  fi
done

echo
echo "== claim 2: remove an entry =="
manifest deployed
before_i=$(icount)
out=$( cd "$PROJ" && "$RIMSKY_BIN" compose up --yes 2>&1 | head -1 )
echo "     instance entry dropped while the instance is live → $out"
if [ "$(icount)" -lt "$before_i" ]; then
  pass "removing the entry removed the instance"
else
  bad "removing the entry did not remove the live instance — compose refuses until it is terminal"
fi
for id in $("$RIMSKY_BIN" instance list -o json | python3 -c '
import json,sys
print(" ".join(i["id"] for i in (json.load(sys.stdin) or []) if not i.get("terminated_at")))'); do
  "$RIMSKY_BIN" instance kill "$id" --force >/dev/null 2>&1
done
( cd "$PROJ" && "$RIMSKY_BIN" compose up --yes ) >/dev/null 2>&1
echo "     after terminating it and re-running up: $(icount) instances left"
printf 'project: state-key\n' > "$PROJ/rimsky-compose.yml"
( cd "$PROJ" && "$RIMSKY_BIN" compose up --yes ) 2>&1 | tail -1
echo "     template entry dropped → $(tcount) templates left (state $(tstate))"
if [ "$(tcount)" = 0 ]; then
  pass "removing the template entry removed the template"
else
  bad "removing the template entry left $(tcount) template(s) behind"
fi

echo
if [ $fail -eq 0 ]; then echo "RESULT: PASS"; else echo "RESULT: FAIL"; fi
exit $fail
