#!/bin/bash
# Experiment: assumption admin-reset-is-scoped.
#
# Boots one rimsky-all-in-one, seeds a template, an instance, a tag and a
# node, then asks `rimsky admin reset` what it targets -- with no argument,
# with two, with an instance id, with a node id -- and runs it on a real pty
# with "n" waiting on stdin so any confirmation prompt would be seen and
# refused. The whole deployment is counted before and after to establish
# whether anything beyond the target moved.
#
# Requires: docker, python3, a rimsky CLI binary (RIMSKY_BIN), RIMSKY_IMAGE_TAG.
set -u

RIMSKY_BIN=${RIMSKY_BIN:?set RIMSKY_BIN to the rimsky CLI binary}
TAG=${RIMSKY_IMAGE_TAG:?set RIMSKY_IMAGE_TAG}
free_port() { python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()'; }
PORT=${PORT:-$(free_port)}
NAME=exp-assumption-admin-reset-is-scoped
BASE="http://127.0.0.1:$PORT"

HERE=$(cd "$(dirname "$0")" && pwd)
WORK=$(mktemp -d); export HOME="$WORK/home"; mkdir -p "$HOME"
unset RIMSKY_API_KEY RIMSKY_CONTEXT
cleanup() { docker rm -f "$NAME" >/dev/null 2>&1; }
trap cleanup EXIT

docker rm -f "$NAME" >/dev/null 2>&1
docker run -d --name "$NAME" -p "$PORT:8080" "rimsky-all-in-one:$TAG" >/dev/null || exit 1
for _ in $(seq 1 90); do
  [ "$(curl -s -m 2 -o /dev/null -w '%{http_code}' "$BASE/v1/health")" = 200 ] && break; sleep 1
done
[ "$(curl -s -m 2 -o /dev/null -w '%{http_code}' "$BASE/v1/health")" = 200 ] || { echo "FAIL  stack did not come up"; exit 1; }
export RIMSKY_CONTROL_API_URL="$BASE"

cat > "$WORK/t.yml" <<'EOF'
name: admin-reset-probe
version: "1"
nodes:
  - type: a
    executor: verifier-shape-checks
  - type: b
    executor: verifier-shape-checks
EOF
H=$("$RIMSKY_BIN" template register "$WORK/t.yml" -o json | python3 -c 'import json,sys;print(json.load(sys.stdin)["template_id"])')
"$RIMSKY_BIN" template deploy "$H" >/dev/null
"$RIMSKY_BIN" tag create probe-tag --template "$H" >/dev/null
I=$("$RIMSKY_BIN" instance create "$H" -o json | python3 -c 'import json,sys;print(json.load(sys.stdin)["instance_id"])')
N=$("$RIMSKY_BIN" instance nodes "$I" -o json | python3 -c 'import json,sys;print(json.load(sys.stdin)[0]["id"])')
echo "instance $I  node $N"
echo

python3 "$HERE/probe.py" "$RIMSKY_BIN" "$BASE" "$HOME" "$I" "$N"
