#!/bin/bash
# Experiment: story dry-run-mode-floor.
#
# Mints three keys through the public auth verbs and has each try the same
# write:
#
#   attempt-only  tag:create pinned to dry_run  -> must never mutate, even
#                 when the holder asks for a real write
#   execute       tag:create unpinned           -> control: mutates
#   mixed         tag:create pinned to dry_run  -> the story's proviso: the
#                 plus tag:* unpinned              floor does not apply
#
# Requires: docker, python3, a rimsky CLI binary (RIMSKY_BIN), RIMSKY_IMAGE_TAG.
set -u

RIMSKY_BIN=${RIMSKY_BIN:?set RIMSKY_BIN to the rimsky CLI binary}
TAG=${RIMSKY_IMAGE_TAG:?set RIMSKY_IMAGE_TAG}
PORT=${PORT:-18126}
NAME=rimsky-exp-mode-floor
BASE="http://127.0.0.1:$PORT"

HERE=$(cd "$(dirname "$0")" && pwd)
WORK=$(mktemp -d)
export HOME="$WORK/home"; mkdir -p "$HOME"
unset RIMSKY_CONTROL_API_URL RIMSKY_API_KEY RIMSKY_CONTEXT
cd "$WORK" || exit 1

cleanup() { docker rm -f "$NAME" >/dev/null 2>&1; }
trap cleanup EXIT

docker rm -f "$NAME" >/dev/null 2>&1
docker run -d --name "$NAME" -p "$PORT:8080" "rimsky-all-in-one:$TAG" >/dev/null || exit 1
for _ in $(seq 1 90); do
  [ "$(curl -s -m 2 -o /dev/null -w '%{http_code}' "$BASE/v1/health")" = 200 ] && break; sleep 1
done

ADMIN=$("$RIMSKY_BIN" auth init --endpoint "$BASE" | awk 'NF==1 && length($1)>20 {print $1; exit}')
[ -n "$ADMIN" ] || { echo "FAIL  could not mint the admin key"; exit 1; }

cat > role-attempt-only.json <<'EOF'
{
  "name": "attempt-only",
  "description": "May attempt a tag create; the grant pins it to dry-run.",
  "permissions": [
    { "action": "tag:create", "mode": "dry_run" },
    { "action": "tag:read" },
    { "action": "template:read" }
  ]
}
EOF
cat > role-execute.json <<'EOF'
{
  "name": "execute-control",
  "description": "Same action, no mode pin.",
  "permissions": [
    { "action": "tag:create" },
    { "action": "tag:read" },
    { "action": "template:read" }
  ]
}
EOF
cat > role-mixed.json <<'EOF'
{
  "name": "mixed",
  "description": "A dry-run pin plus a second grant that authorizes execute on the same action.",
  "permissions": [
    { "action": "tag:create", "mode": "dry_run" },
    { "action": "tag:*" },
    { "action": "template:read" }
  ]
}
EOF

mint() { "$RIMSKY_BIN" auth create-key --name="$1" --role-file="$2" \
  --endpoint "$BASE" --key "$ADMIN" | awk 'NF==1 && length($1)>20 {print $1; exit}'; }

ATTEMPT=$(mint attempt-only role-attempt-only.json)
EXECUTE=$(mint execute-control role-execute.json)
MIXED=$(mint mixed role-mixed.json)
for v in "$ATTEMPT" "$EXECUTE" "$MIXED"; do
  [ -n "$v" ] || { echo "FAIL  a key could not be minted"; exit 1; }
done

python3 "$HERE/probe.py" "$BASE" "$ADMIN" "$ATTEMPT" "$EXECUTE" "$MIXED"
