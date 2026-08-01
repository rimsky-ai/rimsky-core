#!/usr/bin/env bash
# Copyright © 2026 Fall Guy Consulting.
# SPDX-License-Identifier: Apache-2.0

# @story: bundled-park-resume-recipe

set -euo pipefail

ALL_IN_ONE_IMAGE="${ALL_IN_ONE_IMAGE:-rimsky-all-in-one:latest}"
RATELIMIT_IMAGE="${RATELIMIT_IMAGE:-rimsky-example/park-ratelimiter:latest}"
RETRY_AFTER_SECONDS="${RETRY_AFTER_SECONDS:-5}"

for bin in docker curl python3; do
    if ! command -v "${bin}" >/dev/null 2>&1; then
        echo "park-resume-demo: missing required binary: ${bin}" >&2
        exit 1
    fi
done
for image in "${ALL_IN_ONE_IMAGE}" "${RATELIMIT_IMAGE}"; do
    if ! docker image inspect "${image}" >/dev/null 2>&1; then
        echo "park-resume-demo: image ${image} not found locally —" >&2
        echo "  run 'make core-images test-images' first" >&2
        exit 1
    fi
done

RUN_ID="$( date +%s )-$$"
NET="rimsky-park-resume-demo-${RUN_ID}"
RATELIMIT="rimsky-park-resume-demo-ratelimit-${RUN_ID}"
RIMSKY="rimsky-park-resume-demo-rimsky-${RUN_ID}"

cleanup() {
    local rc=$?
    docker rm -f "${RATELIMIT}" "${RIMSKY}" >/dev/null 2>&1 || true
    docker network rm "${NET}" >/dev/null 2>&1 || true
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

echo "park-resume-demo: [1/5] booting the stack (network ${NET})"
docker network create "${NET}" >/dev/null

docker run -d --name "${RATELIMIT}" \
    --network "${NET}" --network-alias park-ratelimit \
    -e RATELIMIT_RETRY_AFTER_SECONDS="${RETRY_AFTER_SECONDS}" \
    "${RATELIMIT_IMAGE}" >/dev/null

docker run -d --name "${RIMSKY}" \
    --network "${NET}" --network-alias rimsky \
    -e RIMSKY_EXECUTOR_HTTP_NODE_EGRESS_ALLOWLIST=0.0.0.0/0 \
    -p 127.0.0.1:0:8080 \
    "${ALL_IN_ONE_IMAGE}" >/dev/null

PORT="$( docker port "${RIMSKY}" 8080 | head -n1 | sed 's/.*://' )"
BASE="http://127.0.0.1:${PORT}"

for _ in $( seq 1 120 ); do
    if curl -fsS "${BASE}/v1/health" >/dev/null 2>&1; then break; fi
    sleep 0.5
done
if ! curl -fsS "${BASE}/v1/health" >/dev/null 2>&1; then
    echo "park-resume-demo: rimsky never became healthy at ${BASE}" >&2
    docker logs "${RIMSKY}" >&2 || true
    exit 1
fi

echo "park-resume-demo: [2/5] registering + deploying the template (bundled http-node → rate-limit-once endpoint)"
TEMPLATE_BODY='{
  "spec": {
    "name": "park-resume-demo",
    "version": "1",
    "nodes": [
      {
        "type": "fetch-report",
        "executor": "http-node",
        "attributes": {
          "schema": {
            "type": "object",
            "properties": {
              "url": { "type": "string", "default": "http://park-ratelimit:9420/report" }
            }
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
    echo "park-resume-demo: template registration failed: ${TPL_RESP}" >&2
    exit 1
fi
curl -fsS -X POST -H 'Content-Type: application/json' -d '{}' \
    "${BASE}/v1/templates/${TEMPLATE_ID}/deploy" >/dev/null

INSTANCE_BODY="$( printf '{"template":"%s","instance_key":"park-resume-1","target_agent":"demo-agent","params":{}}' "${TEMPLATE_ID}" )"
INST_RESP="$( curl -sS -X POST -H 'Content-Type: application/json' \
    -d "${INSTANCE_BODY}" \
    "${BASE}/v1/instances" )"
INSTANCE_ID="$( echo "${INST_RESP}" | json_get "d['instance_id']" )"
if [ -z "${INSTANCE_ID}" ]; then
    echo "park-resume-demo: instance creation failed: ${INST_RESP}" >&2
    exit 1
fi

echo "park-resume-demo: [3/5] watching the node park (first request answers 429, Retry-After: ${RETRY_AFTER_SECONDS})"
PARKED_SEEN=""
LAST_STATE=""
NODE_STATE=""
for _ in $( seq 1 480 ); do
    NODE_STATE="$( curl -fsS "${BASE}/v1/observability/nodes/${INSTANCE_ID}/fetch-report" 2>/dev/null \
        | json_get "d['node']['state']" || true )"
    if [ -n "${NODE_STATE}" ] && [ "${NODE_STATE}" != "${LAST_STATE}" ]; then
        echo "park-resume-demo:       node state → ${NODE_STATE}"
        LAST_STATE="${NODE_STATE}"
    fi
    if [ "${NODE_STATE}" = "parked" ]; then PARKED_SEEN="yes"; fi
    if [ "${NODE_STATE}" = "fresh" ]; then break; fi
    if [ "${NODE_STATE}" = "failed" ]; then
        echo "park-resume-demo: node run FAILED (expected a park, then a resumed success)" >&2
        docker logs "${RIMSKY}" >&2 || true
        exit 1
    fi
    sleep 0.25
done
if [ "${NODE_STATE}" != "fresh" ]; then
    echo "park-resume-demo: node never reached terminal; last state '${NODE_STATE}'" >&2
    docker logs "${RIMSKY}" >&2 || true
    exit 1
fi
if [ -n "${PARKED_SEEN}" ]; then
    echo "park-resume-demo:       observed the parked state live before the timed wake"
fi

echo "park-resume-demo: [4/5] checking the audit ledger for the park and the resumed success"
PARK_EVENTS="$( curl -fsS "${BASE}/v1/observability/events?instance_id=${INSTANCE_ID}&kind=transient/park" \
    | json_get "len(d['events'])" )"
SUCCESS_EVENTS="$( curl -fsS "${BASE}/v1/observability/events?instance_id=${INSTANCE_ID}&kind=terminal/success" \
    | json_get "len(d['events'])" )"
if [ -z "${PARK_EVENTS}" ] || [ "${PARK_EVENTS}" = "0" ]; then
    echo "park-resume-demo: FAIL — no transient/park audit event; the 429 did not travel the production parking path" >&2
    exit 1
fi
if [ -z "${SUCCESS_EVENTS}" ] || [ "${SUCCESS_EVENTS}" = "0" ]; then
    echo "park-resume-demo: FAIL — no terminal/success audit event; the parked run never resumed to success" >&2
    exit 1
fi

echo "park-resume-demo: [5/5] PASS — the node parked on the 429 (transient/park in the audit"
echo "park-resume-demo:        ledger), the supervisor woke it at the Retry-After time,"
echo "park-resume-demo:        re-dispatched it, and the retry settled terminal/success."
