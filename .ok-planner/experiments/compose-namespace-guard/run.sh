#!/bin/bash
# Experiment: story compose-namespace-guard.
#
# Two deployments, because the reservation behaves differently in each:
#
#   authenticated stack -- an operator key holding every ordinary operational
#     grant but not the compose-origin capability tries to create
#     compose-prefixed tags and instance keys through the HTTP API, the MCP
#     JSON-RPC surface, and the CLI, with and without a spoofed origin header.
#
#   unauthenticated stack -- the shipped all-in-one default, and the only
#     posture in which the compose CLI itself works (it sends no credential).
#     An ordinary HTTP client tries the same creations, declaring itself
#     compose by setting the origin header.
#
# The story claims any other client is refused at the server regardless of the
# client surface, so both legs must refuse. The run reports what happens.
#
# Requires: docker, python3, a rimsky CLI binary (RIMSKY_BIN), RIMSKY_IMAGE_TAG.
set -u

RIMSKY_BIN=${RIMSKY_BIN:?set RIMSKY_BIN to the rimsky CLI binary}
TAG=${RIMSKY_IMAGE_TAG:?set RIMSKY_IMAGE_TAG}
free_port() { python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()'; }
PORT_AUTH=${PORT_AUTH:-$(free_port)}
PORT_ANON=${PORT_ANON:-$(free_port)}
NAME_AUTH=rimsky-exp-guard-auth
NAME_ANON=rimsky-exp-guard-anon
BASE_AUTH="http://127.0.0.1:$PORT_AUTH"
BASE_ANON="http://127.0.0.1:$PORT_ANON"

HERE=$(cd "$(dirname "$0")" && pwd)
WORK=$(mktemp -d)
export HOME="$WORK/home"; mkdir -p "$HOME"
unset RIMSKY_CONTROL_API_URL RIMSKY_API_KEY RIMSKY_CONTEXT
cd "$WORK" || exit 1

cleanup() { docker rm -f "$NAME_AUTH" "$NAME_ANON" >/dev/null 2>&1; }
trap cleanup EXIT
fail=0

boot() {
  docker rm -f "$1" >/dev/null 2>&1
  docker run -d --name "$1" -p "$2:8080" "rimsky-all-in-one:$TAG" >/dev/null || exit 1
  for _ in $(seq 1 90); do
    [ "$(curl -s -m 2 -o /dev/null -w '%{http_code}' "http://127.0.0.1:$2/v1/health")" = 200 ] && return 0
    sleep 1
  done
  echo "FAIL  boot $1"; exit 1
}

boot "$NAME_AUTH" "$PORT_AUTH"
boot "$NAME_ANON" "$PORT_ANON"

echo "############ leg 1: authenticated deployment ############"
ADMIN=$("$RIMSKY_BIN" auth init --endpoint "$BASE_AUTH" | awk 'NF==1 && length($1)>20 {print $1; exit}')
[ -n "$ADMIN" ] || { echo "FAIL  could not mint the admin key"; exit 1; }
OPERATOR=$("$RIMSKY_BIN" auth create-key --name=intruder --role=operator \
  --endpoint "$BASE_AUTH" --key "$ADMIN" | awk 'NF==1 && length($1)>20 {print $1; exit}')
[ -n "$OPERATOR" ] || { echo "FAIL  could not mint the operator key"; exit 1; }

python3 "$HERE/probe.py" "$BASE_AUTH" "$ADMIN" "$OPERATOR" || fail=1

echo "-- CLI surface --"
out=$("$RIMSKY_BIN" tag create compose:intruder:sneaky@1 \
      --template sha256-0000000000000000000000000000000000000000000000000000000000000000 \
      --endpoint "$BASE_AUTH" --key "$OPERATOR" 2>&1)
if printf '%s' "$out" | grep -qF 'reserved prefix'; then
  echo "PASS  cli tag create with compose prefix refused: $out"
else
  echo "FAIL  cli tag create with compose prefix was not refused: $out"; fail=1
fi

echo
echo "############ leg 2: unauthenticated deployment (all-in-one default) ############"
python3 "$HERE/probe-anonymous.py" "$BASE_ANON" || fail=1

echo "-- the compose machinery on the same deployment --"
cat > template.yml <<'EOF'
name: legit-compose-template
version: "1"
nodes:
  - type: verify
    executor: verifier-shape-checks
EOF
cat > rimsky-compose.yml <<'EOF'
project: guard-control

templates:
  - path: ./template.yml
    tag: only@1
    state: deployed

instances:
  - template: only@1
    name: one
EOF
"$RIMSKY_BIN" compose up -f rimsky-compose.yml --endpoint "$BASE_ANON" --yes >/dev/null 2>&1
tags=$(curl -s "$BASE_ANON/v1/tags")
if printf '%s' "$tags" | grep -qF 'compose:guard-control:only@1'; then
  echo "PASS  compose created compose:guard-control:only@1 on the unauthenticated deployment"
else
  echo "FAIL  compose could not create its own namespaced tag"; fail=1
fi

echo
if [ $fail -eq 0 ]; then echo "RESULT: PASS"; else echo "RESULT: FAIL"; fi
exit $fail
