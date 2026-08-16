#!/bin/bash
# Experiment: story compose-namespace-guard, second way.
#
# The reservation holds on an authenticated deployment and does not hold on an
# unauthenticated one (run.sh). That leaves one question: can an operator
# simply enable authentication and keep using compose? This way answers it by
# driving the compose verbs at an authenticated deployment with a valid admin
# key, through every key-passing mechanism the CLI offers, with an ordinary
# verb as the control.
#
# Requires: docker, a rimsky CLI binary (RIMSKY_BIN), RIMSKY_IMAGE_TAG.
set -u

RIMSKY_BIN=${RIMSKY_BIN:?set RIMSKY_BIN to the rimsky CLI binary}
TAG=${RIMSKY_IMAGE_TAG:?set RIMSKY_IMAGE_TAG}
free_port() { python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()'; }
PORT=${PORT:-$(free_port)}
NAME=exp-compose-namespace-guard-auth
BASE="http://127.0.0.1:$PORT"

WORK=$(mktemp -d)
export HOME="$WORK/home"; mkdir -p "$HOME"
unset RIMSKY_CONTROL_API_URL RIMSKY_API_KEY RIMSKY_CONTEXT
cd "$WORK" || exit 1

cleanup() { docker rm -f "$NAME" >/dev/null 2>&1; }
trap cleanup EXIT
fail=0

docker rm -f "$NAME" >/dev/null 2>&1
docker run -d --name "$NAME" -p "$PORT:8080" "rimsky-all-in-one:$TAG" >/dev/null || exit 1
until [ "$(curl -s -m 2 -o /dev/null -w '%{http_code}' "$BASE/v1/health")" = 200 ]; do sleep 0.5; done

ADMIN=$("$RIMSKY_BIN" auth init --endpoint "$BASE" | awk 'NF==1 && length($1)>20 {print $1; exit}')
[ -n "$ADMIN" ] || { echo "FAIL  could not mint the admin key"; exit 1; }

cat > template.yml <<'EOF'
name: authed-compose-template
version: "1"
nodes:
  - type: verify
    executor: verifier-shape-checks
EOF
cat > rimsky-compose.yml <<'EOF'
project: authed
templates:
  - path: ./template.yml
    tag: only@1
    state: deployed
instances:
  - template: only@1
    name: one
EOF

refused() { # refused <label> <output>
  if printf '%s' "$2" | grep -qF '401 unauthorized'; then
    echo "REFUSED  $1: $(printf '%s' "$2" | tail -1)"
  else
    echo "ACCEPTED $1: $(printf '%s' "$2" | tail -1)"
  fi
}

echo "== the compose verbs against an authenticated deployment, with a valid admin key =="
refused "compose plan --key"       "$("$RIMSKY_BIN" compose plan -f rimsky-compose.yml --endpoint "$BASE" --key "$ADMIN" 2>&1)"
refused "compose up --key"         "$("$RIMSKY_BIN" compose up -f rimsky-compose.yml --endpoint "$BASE" --key "$ADMIN" --yes 2>&1)"
refused "compose up RIMSKY_API_KEY" "$(RIMSKY_API_KEY="$ADMIN" "$RIMSKY_BIN" compose up -f rimsky-compose.yml --endpoint "$BASE" --yes 2>&1)"

echo "== control: an ordinary verb authenticates with that same key =="
out=$("$RIMSKY_BIN" ls tags --endpoint "$BASE" --key "$ADMIN" 2>&1)
if printf '%s' "$out" | grep -qF '401'; then
  echo "FAIL  the control verb also failed to authenticate: $out"; fail=1
else
  echo "PASS  the ordinary verb authenticates with the same key"
fi

echo "== nothing compose declared exists =="
landed=$(curl -s -H "Authorization: Bearer $ADMIN" "$BASE/v1/tags")
if printf '%s' "$landed" | grep -qF 'compose:authed:'; then
  echo "PASS  compose applied its manifest on the authenticated deployment"
else
  echo "OBSERVED  compose applied nothing on the authenticated deployment: $landed"
fi

echo
echo "OBSERVATION: the compose verbs send no credential, so an operator cannot"
echo "keep the reservation by enabling authentication — enabling it removes the"
echo "compose verbs instead."
exit "$fail"
