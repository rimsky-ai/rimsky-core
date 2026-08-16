#!/bin/bash
# Experiment: assumption template-lint-equals-registration-validation.
#
# Two questions:
#   1. Can `rimsky template lint` run offline? It is asked to lint a valid
#      template with the endpoint pointed at a closed port.
#   2. Where it can run, does it report what registration reports? Nine
#      defective templates -- an undeclared executor, an undeclared claim
#      producer, a duplicate node type, a dangling subscribe, an
#      uncompilable params_schema, an undeclared sent message, an out-of-range
#      duration, an unknown top-level key, a dangling graph reference -- are
#      put through `template lint` and `template register` against the same
#      live deployment and the findings compared.
#
# Requires: docker, python3, a rimsky CLI binary (RIMSKY_BIN), RIMSKY_IMAGE_TAG.
set -u

RIMSKY_BIN=${RIMSKY_BIN:?set RIMSKY_BIN to the rimsky CLI binary}
TAG=${RIMSKY_IMAGE_TAG:?set RIMSKY_IMAGE_TAG}
free_port() { python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()'; }
PORT=${PORT:-$(free_port)}
NAME=exp-assumption-template-lint-equals-registration-validation
BASE="http://127.0.0.1:$PORT"

HERE=$(cd "$(dirname "$0")" && pwd)
WORK=$(mktemp -d); export HOME="$WORK/home"; mkdir -p "$HOME"
unset RIMSKY_API_KEY RIMSKY_CONTEXT
cleanup() { docker rm -f "$NAME" >/dev/null 2>&1; }
trap cleanup EXIT

fail=0

cat > "$WORK/valid.yml" <<'EOF'
name: lint-probe-valid
version: "1"
nodes:
  - type: a
    executor: verifier-shape-checks
EOF

echo "== can lint run without a deployment? =="
out=$(RIMSKY_CONTROL_API_URL=http://127.0.0.1:1 "$RIMSKY_BIN" template lint "$WORK/valid.yml" 2>&1 | head -1)
rc=$?
echo "     rimsky template lint <valid.yml> (no deployment) → $out"
if printf '%s' "$out" | grep -qi 'connection refused\|dial tcp'; then
  echo "FAIL  lint cannot run offline: it POSTs the template to the control API"
  fail=1
else
  echo "PASS  lint runs offline"
fi

echo
echo "== booting a deployment for the comparison =="
docker rm -f "$NAME" >/dev/null 2>&1
docker run -d --name "$NAME" -p "$PORT:8080" "rimsky-all-in-one:$TAG" >/dev/null || exit 1
for _ in $(seq 1 90); do
  [ "$(curl -s -m 2 -o /dev/null -w '%{http_code}' "$BASE/v1/health")" = 200 ] && break; sleep 1
done
[ "$(curl -s -m 2 -o /dev/null -w '%{http_code}' "$BASE/v1/health")" = 200 ] || { echo "FAIL  stack did not come up"; exit 1; }
export RIMSKY_CONTROL_API_URL="$BASE"

python3 "$HERE/compare.py" "$RIMSKY_BIN" "$WORK" || fail=1

echo
if [ $fail -eq 0 ]; then echo "RESULT: PASS"; else echo "RESULT: FAIL"; fi
exit $fail
