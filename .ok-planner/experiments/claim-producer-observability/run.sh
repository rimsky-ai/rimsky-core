#!/usr/bin/env bash
# Experiment: claim-producer-observability
# The bundled filesystem claim producer runs as its own container with a pick
# policy and a seeded queue, and a dashboard-shaped gRPC client drives the
# producer's observability protocol against it: capabilities, paginated claim
# inventory, one claim's full detail, a live stream across a state change, and
# the two admin views the producer itself declares. A rimsky stack pointed at
# the same producer shows the control API carrying those declarations, so a
# dashboard discovers them without a backplane of its own.
set -uo pipefail

TAG="${RIMSKY_IMAGE_TAG:?set RIMSKY_IMAGE_TAG to the image tag built from this tree}"
HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../../.." && pwd)"
CLI="$ROOT/bin/rimsky"
CP_NAME="rimsky-exp-cpo-producer"
STACK_NAME="rimsky-exp-cpo-stack"
CP_PORT="${CP_PORT:-19450}"
PORT="${PORT:-18204}"
E="http://127.0.0.1:$PORT"
WS="$(mktemp -d)"
WORK="$(mktemp -d)"

fails=0
ok()  { echo "PASS  $*"; }
bad() { echo "FAIL  $*"; fails=$((fails+1)); }
has() { if printf '%s' "$2" | grep -qF -- "$1"; then ok "$3"; else bad "$3 (missing '$1')"; fi; }

cleanup() {
  docker rm -f "$CP_NAME" "$STACK_NAME" >/dev/null 2>&1
  rm -rf "$WS" "$WORK"
}
trap cleanup EXIT

for p in "$CP_PORT" "$PORT"; do
  if nc -z 127.0.0.1 "$p" >/dev/null 2>&1; then echo "port $p already in use" >&2; exit 2; fi
done

mkdir -p "$WS/queue/job-1" "$WS/queue/job-2" "$WS/data"
printf 'one\n' > "$WS/queue/job-1/payload.txt"
printf 'two\n' > "$WS/queue/job-2/payload.txt"

docker rm -f "$CP_NAME" >/dev/null 2>&1
docker run -d --name "$CP_NAME" -p "127.0.0.1:$CP_PORT:9100" \
  -e RIMSKY_CLAIM_PRODUCER_FILESYSTEM_CONFIG=/etc/store/config.yml \
  -v "$HERE/claim-producer-filesystem.yml:/etc/store/config.yml:ro" \
  -v "$WS:/workspace:rw" \
  "rimsky-claim-producer-filesystem:$TAG" >/dev/null
until nc -z 127.0.0.1 "$CP_PORT" >/dev/null 2>&1; do sleep 0.2; done

echo "--- a dashboard drives the producer's observability protocol directly"
go build -o "$WORK/dashboard" "$HERE" || { echo "build failed"; exit 1; }
"$WORK/dashboard" -endpoint "127.0.0.1:$CP_PORT" -pick-selector '@queue'
probe=$?
until [ -d "$WS/.fs-store/queue" ]; do sleep 0.2; done
ok "the producer's pick-policy state is on the mounted root: $(cd "$WS/.fs-store/queue" && find . | sort | tr '\n' ' ')"
[ "$probe" -eq 0 ] && ok "every observability probe passed" || bad "the observability probe reported $probe failing checks"

echo "--- a rimsky stack carries the producer's declarations to the control API"
docker rm -f "$STACK_NAME" >/dev/null 2>&1
docker run -d --name "$STACK_NAME" -p "127.0.0.1:$PORT:8080" \
  -v "$HERE/rimsky.yml:/etc/rimsky/rimsky.yml:ro" \
  "rimsky-all-in-one:$TAG" >/dev/null
until "$CLI" health --endpoint "$E" >/dev/null 2>&1; do sleep 0.2; done
entry=""
until [ -n "$entry" ]; do
  entry="$(curl -sS "$E/v1/observability/claim-producers/file-store" 2>/dev/null)"
  printf '%s' "$entry" | grep -q 'reachability_status' || entry=""
  [ -n "$entry" ] || sleep 0.3
done
echo "    $entry"
has '"reachability_status":"reachable"' "$entry" "the control API reaches the producer's observability service"
has '"supports_claim_get":true' "$entry" "the control API carries the producer's claim-detail capability"
has '"supports_claim_stream":true' "$entry" "the control API carries the producer's claim-stream capability"
has '"supports_list_claims":true' "$entry" "the control API carries the producer's list-claims capability"
has '"name":"pick_policies"' "$entry" "the control API carries the producer's first declared admin view"
has '"name":"policy_items"' "$entry" "the control API carries the producer's second declared admin view"

echo
if [ "$fails" -eq 0 ]; then echo "EXPERIMENT PASS"; else echo "EXPERIMENT FAIL ($fails)"; fi
exit "$fails"
