#!/bin/bash
# Experiment: assumption params-redact-applies-everywhere.
#
# Registers a template whose params_schema declares a secret param and whose
# params_redact names it, creates an instance carrying a distinctive literal,
# drives a node run so frames and events exist, then reads every public
# surface where the value could surface and greps each response for the
# literal. The population is the surfaces the prior names -- the instance
# read, the event log, the audit log -- plus the frame reads, the listing, the
# observability views the same key can reach, and the deployment's own process
# logs.
#
# Requires: docker, python3, a rimsky CLI binary (RIMSKY_BIN), RIMSKY_IMAGE_TAG.
set -u

RIMSKY_BIN=${RIMSKY_BIN:?set RIMSKY_BIN to the rimsky CLI binary}
TAG=${RIMSKY_IMAGE_TAG:?set RIMSKY_IMAGE_TAG}
free_port() { python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()'; }
PORT=${PORT:-$(free_port)}
NAME=exp-assumption-params-redact-applies-everywhere
BASE="http://127.0.0.1:$PORT"
SECRET=RIMSKYPROBESECRETLITERAL

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

cat > "$WORK/t.yml" <<'EOF'
name: params-redact-probe
version: "1"
params_schema:
  type: object
  properties:
    secret: { type: string }
    public: { type: string }
params_redact: [secret]
nodes:
  - type: a
    executor: verifier-shape-checks
EOF
H=$("$RIMSKY_BIN" template register "$WORK/t.yml" -o json | python3 -c 'import json,sys;print(json.load(sys.stdin)["template_id"])')
"$RIMSKY_BIN" template deploy "$H" >/dev/null
I=$("$RIMSKY_BIN" instance create "$H" --params "{\"secret\":\"$SECRET\",\"public\":\"visible\"}" -o json \
     | python3 -c 'import json,sys;print(json.load(sys.stdin)["instance_id"])')
N=$("$RIMSKY_BIN" instance nodes "$I" -o json | python3 -c 'import json,sys;print(json.load(sys.stdin)[0]["id"])')
"$RIMSKY_BIN" admin reset "$N" >/dev/null 2>&1
for _ in $(seq 1 20); do
  [ "$(curl -s -H "Authorization: Bearer $ADMIN" "$BASE/v1/audit" | grep -c "$SECRET")" != "" ] && break
done
echo "instance $I  node $N"

SURFACES=(
  "/v1/instances/$I"
  "/v1/instances"
  "/v1/instances/$I/frames"
  "/v1/instances/$I/nodes"
  "/v1/events?instance_id=$I"
  "/v1/events"
  "/v1/audit"
  "/v1/observability/instances/$I"
  "/v1/observability/instances"
  "/v1/observability/events"
  "/v1/observability/frames"
  "/v1/observability/node-runs"
  "/v1/nodes/$N"
)
echo
echo "== ${#SURFACES[@]} public reads, each grepped for the literal =="
leaks=()
for s in "${SURFACES[@]}"; do
  body=$(curl -s -H "Authorization: Bearer $ADMIN" "$BASE$s")
  n=$(printf '%s' "$body" | grep -c "$SECRET")
  if [ "$n" != 0 ]; then
    leaks+=("$s"); printf '  LEAK     %-46s (%s bytes)\n' "$s" "${#body}"
  else
    printf '  redacted %-46s (%s bytes)\n' "$s" "${#body}"
  fi
done
logn=$(docker logs "$NAME" 2>&1 | grep -c "$SECRET")
if [ "$logn" != 0 ]; then
  leaks+=("process logs"); echo "  LEAK     process logs ($logn lines)"
else
  echo "  redacted process logs"
fi

echo
if [ ${#leaks[@]} -eq 0 ]; then
  echo "PASS  the value surfaces nowhere"
else
  echo "FAIL  the value surfaces on ${#leaks[@]} of $(( ${#SURFACES[@]} + 1 )) surfaces: ${leaks[*]}"
  fail=1
  echo
  echo "  the shape that carries it:"
  curl -s -H "Authorization: Bearer $ADMIN" "$BASE/v1/audit" > "$WORK/audit.json"
  SECRET="$SECRET" python3 - "$WORK/audit.json" <<'PY'
import json, os, sys
secret = os.environ["SECRET"]
d = json.load(open(sys.argv[1]))
rows = d if isinstance(d, list) else (d.get("audit") or d.get("events") or d.get("entries") or [])
for r in rows:
    if secret in json.dumps(r):
        print("    kind:", r.get("kind"))
        print("   ", json.dumps(r.get("payload", r))[:260])
        break
PY
fi

echo
if [ $fail -eq 0 ]; then echo "RESULT: PASS"; else echo "RESULT: FAIL"; fi
exit $fail
