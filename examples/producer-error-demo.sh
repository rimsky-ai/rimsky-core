#!/usr/bin/env bash
# Copyright © 2026 Fall Guy Consulting.
# Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
# repo root, or http://www.apache.org/licenses/LICENSE-2.0.

# @story: producer-error-passthrough

set -euo pipefail

ALL_IN_ONE_IMAGE="${ALL_IN_ONE_IMAGE:-rimsky-all-in-one:latest}"
STORE_IMAGE="${STORE_IMAGE:-rimsky-claim-producer-filesystem:latest}"
EXECUTOR_IMAGE="${EXECUTOR_IMAGE:-rimsky-executor-http-node:latest}"

for bin in docker curl python3; do
    if ! command -v "${bin}" >/dev/null 2>&1; then
        echo "producer-error-demo: missing required binary: ${bin}" >&2
        exit 1
    fi
done
for image in "${ALL_IN_ONE_IMAGE}" "${STORE_IMAGE}" "${EXECUTOR_IMAGE}"; do
    if ! docker image inspect "${image}" >/dev/null 2>&1; then
        echo "producer-error-demo: image ${image} not found locally —" >&2
        echo "  run 'make core-images service-images' first" >&2
        exit 1
    fi
done

RUN_ID="$( date +%s )-$$"
NET="rimsky-producer-error-demo-${RUN_ID}"
STORE="rimsky-producer-error-demo-store-${RUN_ID}"
EXECUTOR="rimsky-producer-error-demo-executor-${RUN_ID}"
RIMSKY="rimsky-producer-error-demo-rimsky-${RUN_ID}"
TMP_DIR="$( mktemp -d -t rimsky-producer-error-demo.XXXXXXXX )"

cleanup() {
    local rc=$?
    docker exec "${STORE}" rm -rf /workspace/data >/dev/null 2>&1 || true
    docker rm -f "${STORE}" "${EXECUTOR}" "${RIMSKY}" >/dev/null 2>&1 || true
    docker network rm "${NET}" >/dev/null 2>&1 || true
    rm -rf "${TMP_DIR}" 2>/dev/null || true
    exit "${rc}"
}
trap cleanup EXIT

json_get() {
    python3 -c "
import sys, json
try:
    d = json.load(sys.stdin)
    v = (${1})
    print(v if v is not None else '')
except Exception:
    pass
" 2>/dev/null
}

echo "producer-error-demo: [1/6] booting the stack (network ${NET})"
docker network create "${NET}" >/dev/null

mkdir -p "${TMP_DIR}/workspace/data"
cat > "${TMP_DIR}/store-config.yml" <<'YAML'
root: /workspace/data
host: 0.0.0.0
grpc_port: 9100
http_port: 9110
YAML
docker run -d --name "${STORE}" \
    --network "${NET}" --network-alias store \
    -e STORE_FILESYSTEM_CONFIG=/etc/store/config.yml \
    -v "${TMP_DIR}/store-config.yml:/etc/store/config.yml:ro" \
    -v "${TMP_DIR}/workspace:/workspace:rw" \
    -p 127.0.0.1:0:9110 \
    "${STORE_IMAGE}" >/dev/null
STORE_HTTP_PORT="$( docker port "${STORE}" 9110 | head -n1 | sed 's/.*://' )"
STORE_BRIDGE="http://127.0.0.1:${STORE_HTTP_PORT}"

docker run -d --name "${EXECUTOR}" \
    --network "${NET}" --network-alias executor \
    -e RIMSKY_EXECUTOR_STUB_MODE=1 \
    "${EXECUTOR_IMAGE}" >/dev/null

cat > "${TMP_DIR}/rimsky.yml" <<'YAML'
persistence:
  driver: sqlite
  sqlite:
    path: /var/lib/rimsky/state.db
claim_producers:
  docs:
    endpoint: "grpc://store:9100"
    protocols: [claim_producer]
    write_semantics_allowed: [sync]
named_locks: {}
executors:
  runner:
    transport: grpc
    endpoint: "executor:9091"
    tls: off
    protocols: [executor]
YAML
docker run -d --name "${RIMSKY}" \
    --network "${NET}" --network-alias rimsky \
    -p 127.0.0.1:0:8080 \
    -v "${TMP_DIR}/rimsky.yml:/etc/rimsky/rimsky.yml:ro" \
    "${ALL_IN_ONE_IMAGE}" >/dev/null

PORT="$( docker port "${RIMSKY}" 8080 | head -n1 | sed 's/.*://' )"
BASE="http://127.0.0.1:${PORT}"

for _ in $( seq 1 120 ); do
    if curl -fsS "${BASE}/v1/health" >/dev/null 2>&1; then break; fi
    sleep 0.5
done
if ! curl -fsS "${BASE}/v1/health" >/dev/null 2>&1; then
    echo "producer-error-demo: rimsky never became healthy at ${BASE}" >&2
    docker logs "${RIMSKY}" >&2 || true
    exit 1
fi

echo "producer-error-demo: [2/6] registering + deploying the template (durable claim on store 'docs')"
TEMPLATE_BODY='{
  "spec": {
    "name": "producer-error-demo",
    "version": "1",
    "nodes": [
      {
        "type": "produce-report",
        "executor": "runner",
        "claim_producers": [
          { "name": "docs", "alias": "out", "selector": "reports/out.json",
            "intent": "rw", "lifetime": "durable" }
        ],
        "attributes": {
          "schema": {
            "type": "object",
            "properties": {
              "url": { "type": "string", "source": "{{params.url}}" }
            },
            "required": ["url"]
          }
        }
      }
    ]
  }
}'
TPL_RESP="$( curl -sS -X POST -H 'Content-Type: application/json' \
    -d "${TEMPLATE_BODY}" "${BASE}/v1/templates" )"
TEMPLATE_ID="$( echo "${TPL_RESP}" | json_get "d['template_id']" )"
if [ -z "${TEMPLATE_ID}" ]; then
    echo "producer-error-demo: template registration failed: ${TPL_RESP}" >&2
    exit 1
fi
curl -fsS -X POST -H 'Content-Type: application/json' -d '{}' \
    "${BASE}/v1/templates/${TEMPLATE_ID}/deploy" >/dev/null

INSTANCE_BODY="$( printf '{"template":"%s","instance_key":"demo-1","params":{"url":"http://rimsky:8080/v1/health"}}' "${TEMPLATE_ID}" )"
INST_RESP="$( curl -sS -X POST -H 'Content-Type: application/json' \
    -d "${INSTANCE_BODY}" \
    "${BASE}/v1/instances" )"
INSTANCE_ID="$( echo "${INST_RESP}" | json_get "d['instance_id']" )"
if [ -z "${INSTANCE_ID}" ]; then
    echo "producer-error-demo: instance creation failed: ${INST_RESP}" >&2
    exit 1
fi

echo "producer-error-demo: [3/6] driving the node to terminal (instance ${INSTANCE_ID})"
NODE_STATE=""
for _ in $( seq 1 240 ); do
    NODE_STATE="$( curl -fsS "${BASE}/v1/observability/nodes/${INSTANCE_ID}/produce-report" 2>/dev/null \
        | json_get "d['node']['state']" || true )"
    if [ "${NODE_STATE}" = "fresh" ]; then break; fi
    if [ "${NODE_STATE}" = "failed" ]; then
        echo "producer-error-demo: node run FAILED (expected success while the store is healthy)" >&2
        docker logs "${RIMSKY}" >&2 || true
        exit 1
    fi
    sleep 0.5
done
if [ "${NODE_STATE}" != "fresh" ]; then
    echo "producer-error-demo: node never reached terminal; last state '${NODE_STATE}'" >&2
    docker logs "${RIMSKY}" >&2 || true
    exit 1
fi

ASSET_LIFETIME=""
for _ in $( seq 1 60 ); do
    ASSET_LIFETIME="$( curl -fsS "${BASE}/v1/instances/${INSTANCE_ID}/assets/produce-report.out" 2>/dev/null \
        | json_get "d['lifetime']" || true )"
    if [ "${ASSET_LIFETIME}" = "durable" ]; then break; fi
    sleep 0.5
done
if [ "${ASSET_LIFETIME}" != "durable" ]; then
    echo "producer-error-demo: durable asset never appeared at produce-report.out (lifetime '${ASSET_LIFETIME}')" >&2
    curl -sS "${BASE}/v1/instances/${INSTANCE_ID}/assets/produce-report.out" >&2 || true
    exit 1
fi
echo "producer-error-demo:       durable asset committed: produce-report.out"

echo "producer-error-demo: [4/6] sabotaging the store's backing path (rm -rf of the configured root)"
rm -rf "${TMP_DIR}/workspace/data" 2>/dev/null     || docker exec "${STORE}" rm -rf /workspace/data

SABOTAGE_SEEN=""
for _ in $( seq 1 60 ); do
    PROBE="$( curl -sS -X POST -H 'Content-Type: application/json' \
        -d '{"claim_id":"sabotage-probe","selector":"probe/x","intent":"r"}' \
        "${STORE_BRIDGE}/v1/open" 2>/dev/null || true )"
    case "${PROBE}" in
        *fs/root_unavailable*) SABOTAGE_SEEN="yes"; break ;;
    esac
    sleep 0.5
done
if [ -z "${SABOTAGE_SEEN}" ]; then
    echo "producer-error-demo: store never observed the removed root; last probe: ${PROBE}" >&2
    exit 1
fi

echo "producer-error-demo: [5/6] DELETE the asset — rimsky calls the store's Release, the store rejects"
HTTP_STATUS_FILE="${TMP_DIR}/delete-status"
DELETE_RESP="$( curl -sS -X DELETE -o - -w '%{stderr}%{http_code}' \
    "${BASE}/v1/instances/${INSTANCE_ID}/assets/produce-report.out" \
    2>"${HTTP_STATUS_FILE}" )"
DELETE_STATUS="$( cat "${HTTP_STATUS_FILE}" )"

echo "producer-error-demo:       API response (HTTP ${DELETE_STATUS}):"
echo "${DELETE_RESP}" | python3 -m json.tool | sed 's/^/producer-error-demo:         /'

echo "producer-error-demo: [6/6] asserting the producer's error crossed the HTTP boundary intact"
if [ "${DELETE_STATUS}" != "502" ]; then
    echo "producer-error-demo: FAIL — expected 502 Bad Gateway (producer failed), got ${DELETE_STATUS}" >&2
    exit 1
fi
PRODUCER_NAME="$( echo "${DELETE_RESP}" | json_get "d['producer_name']" )"
ERROR_CLASS="$(   echo "${DELETE_RESP}" | json_get "d['error_class']" )"
MESSAGE="$(       echo "${DELETE_RESP}" | json_get "d['message']" )"
if [ "${PRODUCER_NAME}" != "docs" ]; then
    echo "producer-error-demo: FAIL — body.producer_name should name the operator's store 'docs', got '${PRODUCER_NAME}'" >&2
    exit 1
fi
if [ "${ERROR_CLASS}" != "fs/root_unavailable" ]; then
    echo "producer-error-demo: FAIL — body.error_class should carry the store's own class 'fs/root_unavailable', got '${ERROR_CLASS}'" >&2
    exit 1
fi
case "${MESSAGE}" in
    *"/workspace/data"*"not accessible"*) ;;
    *)
        echo "producer-error-demo: FAIL — body.message should carry the store's own message naming the root, got '${MESSAGE}'" >&2
        exit 1
        ;;
esac

echo "producer-error-demo: PASS — the store's own error class ('${ERROR_CLASS}') and message"
echo "producer-error-demo:        crossed the gRPC → HTTP boundary intact, under 502 (producer"
echo "producer-error-demo:        failed) rather than a bare rimsky-internal 500."
