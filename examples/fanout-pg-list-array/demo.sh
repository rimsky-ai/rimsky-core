#!/usr/bin/env bash
# Copyright © 2026 Fall Guy Consulting.
# SPDX-License-Identifier: Apache-2.0

# @story: pg-fanout-list-array

set -euo pipefail

SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
TEMPLATE_PATH="${SCRIPT_DIR}/template.yaml"

RIMSKY_CONTROL_API_URL="${RIMSKY_CONTROL_API_URL:-http://127.0.0.1:8080}"
POLL_BUDGET_SECONDS="${POLL_BUDGET_SECONDS:-120}"

which yq >/dev/null 2>&1 || { echo "fanout-pg-list-array: yq not on PATH" >&2; exit 2; }
which curl >/dev/null 2>&1 || { echo "fanout-pg-list-array: curl not on PATH" >&2; exit 2; }
which jq >/dev/null 2>&1 || { echo "fanout-pg-list-array: jq not on PATH" >&2; exit 2; }

echo "fanout-pg-list-array: registering template at ${RIMSKY_CONTROL_API_URL}"

SPEC_JSON="$( yq -o=json '.' "${TEMPLATE_PATH}" )"
REGISTER_BODY="$( jq -n --argjson spec "${SPEC_JSON}" '{spec: $spec}' )"
REGISTER_OUT="$( curl -sS -X POST -H 'Content-Type: application/json' \
    --data "${REGISTER_BODY}" \
    "${RIMSKY_CONTROL_API_URL}/v1/templates" )"
TEMPLATE_ID="$( echo "${REGISTER_OUT}" | jq -r '.template_id' )"
if [ -z "${TEMPLATE_ID}" ] || [ "${TEMPLATE_ID}" = "null" ]; then
    echo "fanout-pg-list-array: template register failed: ${REGISTER_OUT}" >&2
    exit 1
fi
echo "fanout-pg-list-array: registered template ${TEMPLATE_ID}"

DEPLOY_OUT="$( curl -sS -X POST -H 'Content-Type: application/json' \
    --data '{}' \
    "${RIMSKY_CONTROL_API_URL}/v1/templates/${TEMPLATE_ID}/deploy" )"
echo "fanout-pg-list-array: template deployed: ${DEPLOY_OUT}"

INSTANCE_KEY="fanout-pg-list-array-$( date +%s )-$$"
INSTANCE_OUT="$( curl -sS -X POST -H 'Content-Type: application/json' \
    --data "{\"template\": \"${TEMPLATE_ID}\", \"instance_key\": \"${INSTANCE_KEY}\", \"target_agent\": \"demo-agent\"}" \
    "${RIMSKY_CONTROL_API_URL}/v1/instances" )"
INSTANCE_ID="$( echo "${INSTANCE_OUT}" | jq -r '.instance_id' )"
if [ -z "${INSTANCE_ID}" ] || [ "${INSTANCE_ID}" = "null" ]; then
    echo "fanout-pg-list-array: instance create failed: ${INSTANCE_OUT}" >&2
    exit 1
fi
echo "fanout-pg-list-array: instance ${INSTANCE_ID} created"

IDEMPOTENCY_KEY="fpla-$( uuidgen 2>/dev/null || echo "${INSTANCE_KEY}" )"
MESSAGE_BODY='{"type":"fanout_seed","payload":{"items":[{"key":"x","payload":{"v":10}},{"key":"y","payload":{"v":20}},{"key":"z","payload":{"v":30}}]}}'
MESSAGE_OUT="$( curl -sS -X POST -H 'Content-Type: application/json' \
    -H "Idempotency-Key: ${IDEMPOTENCY_KEY}" \
    --data "${MESSAGE_BODY}" \
    "${RIMSKY_CONTROL_API_URL}/v1/instances/${INSTANCE_ID}/messages" )"
MESSAGE_ID="$( echo "${MESSAGE_OUT}" | jq -r '.message_id' )"
if [ -z "${MESSAGE_ID}" ] || [ "${MESSAGE_ID}" = "null" ]; then
    echo "fanout-pg-list-array: fanout_seed POST failed: ${MESSAGE_OUT}" >&2
    exit 1
fi
echo "fanout-pg-list-array: posted fanout_seed message ${MESSAGE_ID}"

END=$(( $(date +%s) + POLL_BUDGET_SECONDS ))
SAW_THREE_KEYS=0
SAW_THREE_PAYLOADS=0
LAST_NODES_OUT=""
while [ "$( date +%s )" -lt "${END}" ]; do
    NODES_OUT="$( curl -sS "${RIMSKY_CONTROL_API_URL}/v1/instances/${INSTANCE_ID}/nodes?limit=100" )"
    LAST_NODES_OUT="${NODES_OUT}"
    NODE_IDS="$( echo "${NODES_OUT}" \
        | jq -r '.nodes[] | select(.node_type == "triage") | .id' )"
    if [ -z "${NODE_IDS}" ]; then
        sleep 2
        continue
    fi
    PARTITION_KEYS=()
    PAYLOAD_VS=()
    while IFS= read -r NID; do
        [ -z "${NID}" ] && continue
        DETAIL="$( curl -sS "${RIMSKY_CONTROL_API_URL}/v1/nodes/${NID}" )"
        PK="$( echo "${DETAIL}" | jq -r '.latest_attributes.partition_key // empty' )"
        PV="$( echo "${DETAIL}" | jq -r '.latest_attributes.processed_payload.v // empty' )"
        if [ -n "${PK}" ]; then
            PARTITION_KEYS+=("${PK}")
        fi
        if [ -n "${PV}" ]; then
            PAYLOAD_VS+=("${PV}")
        fi
    done <<< "${NODE_IDS}"

    DISTINCT_KEYS="$( printf '%s\n' "${PARTITION_KEYS[@]:-}" | sort -u | grep -v '^$' | wc -l | tr -d ' ' )"
    DISTINCT_PVS="$( printf '%s\n' "${PAYLOAD_VS[@]:-}" | sort -u | grep -v '^$' | wc -l | tr -d ' ' )"
    if [ "${DISTINCT_KEYS}" -ge 3 ]; then SAW_THREE_KEYS=1; fi
    if [ "${DISTINCT_PVS}" -ge 3 ]; then SAW_THREE_PAYLOADS=1; fi
    if [ "${SAW_THREE_KEYS}" -eq 1 ] && [ "${SAW_THREE_PAYLOADS}" -eq 1 ]; then
        break
    fi
    sleep 2
done

if [ "${SAW_THREE_KEYS}" -eq 0 ]; then
    echo "fanout-pg-list-array: FAIL — did not observe 3 distinct partition_keys within ${POLL_BUDGET_SECONDS}s" >&2
    echo "${LAST_NODES_OUT}" | jq '.' >&2 || true
    exit 1
fi
if [ "${SAW_THREE_PAYLOADS}" -eq 0 ]; then
    echo "fanout-pg-list-array: FAIL — did not observe 3 distinct sub-claim payload echoes within ${POLL_BUDGET_SECONDS}s" >&2
    echo "${LAST_NODES_OUT}" | jq '.' >&2 || true
    exit 1
fi

echo "fanout-pg-list-array: PASS — 3 children dispatched, 3 distinct partition_keys, 3 distinct per-sub-claim payload values"
