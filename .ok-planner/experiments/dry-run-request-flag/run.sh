#!/bin/bash
# Experiment: story dry-run-request-flag.
#
# Walks the control API's write actions and submits each one with the
# per-request dry-run flag, requiring a synthetic envelope, an unchanged
# world, and a live control that proves the request was valid. The population
# is the write half of the control API's action registry, which the run
# reports as a count.
#
# asset:delete is the one write whose subject has to be materialized before it
# can be previewed, so the stack is booted with a claim producer that also
# advertises the data-processing protocol (the one built for asset-management),
# and the probe materializes a durable claim through it first.
#
# Requires: docker, go, python3, a rimsky CLI binary (RIMSKY_BIN), RIMSKY_IMAGE_TAG.
set -u

RIMSKY_BIN=${RIMSKY_BIN:?set RIMSKY_BIN to the rimsky CLI binary}
TAG=${RIMSKY_IMAGE_TAG:?set RIMSKY_IMAGE_TAG}
PORT=${PORT:-18127}
NAME=rimsky-exp-dry-run-flag
PRODUCER=exp-dry-run-flag-producer
NET=exp-dry-run-flag-net
BASE="http://127.0.0.1:$PORT"

HERE=$(cd "$(dirname "$0")" && pwd)
ROOT=$(cd "$HERE/../../.." && pwd)
WORK=$(mktemp -d)
export HOME="$WORK/home"; mkdir -p "$HOME"
unset RIMSKY_CONTROL_API_URL RIMSKY_API_KEY RIMSKY_CONTEXT
cd "$WORK" || exit 1

cleanup() {
  docker rm -f "$NAME" "$PRODUCER" >/dev/null 2>&1
  docker network rm "$NET" >/dev/null 2>&1
}
trap cleanup EXIT

ARCH=$(docker version --format '{{.Server.Arch}}' 2>/dev/null || echo arm64)
( cd "$ROOT" && GOOS=linux GOARCH="$ARCH" CGO_ENABLED=0 \
    go build -o "$WORK/producer" "$HERE/../asset-management" ) || {
  echo "FAIL  could not build the data-processing claim producer"; exit 1; }

cat > "$WORK/rimsky.yml" <<'YAML'
persistence:
  driver: sqlite
  sqlite:
    path: /var/lib/rimsky/state.db
claim_producers:
  content:
    endpoint: "grpc://content-producer:9500"
    tls: "off"
    write_semantics_allowed: ["sync"]
    protocols: ["claim_producer", "data_processing"]
named_locks: {}
executors: {}
YAML

docker rm -f "$NAME" "$PRODUCER" >/dev/null 2>&1
docker network create "$NET" >/dev/null 2>&1
docker run -d --name "$PRODUCER" --network "$NET" --network-alias content-producer \
  -v "$WORK/producer:/exp/producer:ro" alpine:latest /exp/producer -bind 0.0.0.0:9500 >/dev/null || exit 1
docker run -d --name "$NAME" --network "$NET" -p "$PORT:8080" \
  -v "$WORK/rimsky.yml:/etc/rimsky/rimsky.yml:ro" "rimsky-all-in-one:$TAG" >/dev/null || exit 1
for _ in $(seq 1 90); do
  [ "$(curl -s -m 2 -o /dev/null -w '%{http_code}' "$BASE/v1/health")" = 200 ] && break; sleep 1
done

ADMIN=$("$RIMSKY_BIN" auth init --endpoint "$BASE" | awk 'NF==1 && length($1)>20 {print $1; exit}')
[ -n "$ADMIN" ] || { echo "FAIL  could not mint the admin key"; exit 1; }

python3 "$HERE/probe.py" "$BASE" "$ADMIN"
