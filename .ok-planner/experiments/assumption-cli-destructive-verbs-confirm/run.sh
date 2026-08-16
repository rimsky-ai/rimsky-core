#!/bin/bash
# Experiment: assumption cli-destructive-verbs-confirm.
#
# Boots one rimsky-all-in-one, seeds a template, two instances, a tag, and a
# spare api-key, then runs each destructive verb on a real pty with "n"
# already waiting on stdin -- the answer an operator gives when they change
# their mind. After each verb the probe re-reads the subject and reports
# whether it survived. `compose down`, the one family that is known to ask,
# runs as the control. `admin reset` runs last because it wipes the world.
#
# Requires: docker, python3, a rimsky CLI binary (RIMSKY_BIN), RIMSKY_IMAGE_TAG.
set -u

RIMSKY_BIN=${RIMSKY_BIN:?set RIMSKY_BIN to the rimsky CLI binary}
TAG=${RIMSKY_IMAGE_TAG:?set RIMSKY_IMAGE_TAG}
free_port() { python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()'; }
PORT=${PORT:-$(free_port)}
NAME=exp-assumption-cli-destructive
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

python3 "$HERE/probe.py" "$RIMSKY_BIN" "$BASE" "$HOME"
