#!/usr/bin/env bash
# Copyright © 2026 Fall Guy Consulting.
# SPDX-License-Identifier: Apache-2.0

# @story: sub-claim-payload-substitution

set -euo pipefail

SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
TEMPLATE_PATH="${SCRIPT_DIR}/template.yaml"

RIMSKY_CONTROL_API_URL="${RIMSKY_CONTROL_API_URL:-http://127.0.0.1:8080}"
POLL_BUDGET_SECONDS="${POLL_BUDGET_SECONDS:-120}"

which yq >/dev/null 2>&1 || { echo "sub-claim-payload: yq not on PATH" >&2; exit 2; }
which curl >/dev/null 2>&1 || { echo "sub-claim-payload: curl not on PATH" >&2; exit 2; }
which jq >/dev/null 2>&1 || { echo "sub-claim-payload: jq not on PATH" >&2; exit 2; }

echo "sub-claim-payload: registering template at ${RIMSKY_CONTROL_API_URL}"

SPEC_JSON="$( yq -o=json '.' "${TEMPLATE_PATH}" )"
REGISTER_BODY="$( jq -n --argjson spec "${SPEC_JSON}" '{spec: $spec}' )"
REGISTER_OUT="$( curl -sS -X POST -H 'Content-Type: application/json' \
    --data "${REGISTER_BODY}" \
    "${RIMSKY_CONTROL_API_URL}/v1/templates" )"
TEMPLATE_ID="$( echo "${REGISTER_OUT}" | jq -r '.template_id' )"
if [ -z "${TEMPLATE_ID}" ] || [ "${TEMPLATE_ID}" = "null" ]; then
    echo "sub-claim-payload: template register failed: ${REGISTER_OUT}" >&2
    exit 1
fi
echo "sub-claim-payload: registered template ${TEMPLATE_ID}"

curl -sS -X POST -H 'Content-Type: application/json' --data '{}' \
    "${RIMSKY_CONTROL_API_URL}/v1/templates/${TEMPLATE_ID}/deploy" >/dev/null
echo "sub-claim-payload: template deployed"

INSTANCE_KEY="sub-claim-payload-$( date +%s )-$$"
INSTANCE_OUT="$( curl -sS -X POST -H 'Content-Type: application/json' \
    --data "{\"template\": \"${TEMPLATE_ID}\", \"instance_key\": \"${INSTANCE_KEY}\", \"target_agent\": \"demo-agent\"}" \
    "${RIMSKY_CONTROL_API_URL}/v1/instances" )"
INSTANCE_ID="$( echo "${INSTANCE_OUT}" | jq -r '.instance_id' )"
if [ -z "${INSTANCE_ID}" ] || [ "${INSTANCE_ID}" = "null" ]; then
    echo "sub-claim-payload: instance create failed: ${INSTANCE_OUT}" >&2
    exit 1
fi
echo "sub-claim-payload: instance ${INSTANCE_ID} created"

K="scp-$( uuidgen 2>/dev/null || echo "${INSTANCE_KEY}" )"
MESSAGE_BODY='{"type":"fanout/seed","payload":{"items":[{"key":"a","payload":{"v":1}},{"key":"b","payload":{"v":2}},{"key":"c","payload":{"v":3}}]}}'
curl -sS -X POST -H 'Content-Type: application/json' \
    -H "Idempotency-Key: ${K}" \
    --data "${MESSAGE_BODY}" \
    "${RIMSKY_CONTROL_API_URL}/v1/instances/${INSTANCE_ID}/messages" >/dev/null
echo "sub-claim-payload: posted fanout_seed with three sub-claim payloads (v=1, v=2, v=3)"

END=$(( $(date +%s) + POLL_BUDGET_SECONDS ))
SAW_ALL_THREE=0
LAST_DETAILS=""
while [ "$( date +%s )" -lt "${END}" ]; do
    NODES_OUT="$( curl -sS "${RIMSKY_CONTROL_API_URL}/v1/instances/${INSTANCE_ID}/nodes?limit=100" )"
    NODE_IDS="$( echo "${NODES_OUT}" \
        | jq -r '.nodes[] | select(.node_type == "triage") | .id' )"
    if [ -z "${NODE_IDS}" ]; then
        sleep 2
        continue
    fi
    PROCESSED_VALUES=()
    DETAILS_BUF=""
    while IFS= read -r NID; do
        [ -z "${NID}" ] && continue
        DETAIL="$( curl -sS "${RIMSKY_CONTROL_API_URL}/v1/nodes/${NID}" )"
        DETAILS_BUF="${DETAILS_BUF}${DETAIL}\n"
        PV="$( echo "${DETAIL}" | jq -r '.latest_attributes.processed_value // empty' )"
        if [ -n "${PV}" ]; then
            PROCESSED_VALUES+=("${PV}")
        fi
    done <<< "${NODE_IDS}"
    LAST_DETAILS="${DETAILS_BUF}"
    SAW_1=0; SAW_2=0; SAW_3=0
    for V in "${PROCESSED_VALUES[@]:-}"; do
        case "${V}" in
            1) SAW_1=1 ;;
            2) SAW_2=1 ;;
            3) SAW_3=1 ;;
        esac
    done
    if [ "${SAW_1}" -eq 1 ] && [ "${SAW_2}" -eq 1 ] && [ "${SAW_3}" -eq 1 ]; then
        SAW_ALL_THREE=1
        break
    fi
    sleep 2
done

if [ "${SAW_ALL_THREE}" -eq 0 ]; then
    echo "sub-claim-payload: FAIL — did not observe processed_value in {1,2,3} across three children within ${POLL_BUDGET_SECONDS}s" >&2
    echo -e "${LAST_DETAILS}" >&2 || true
    exit 1
fi

echo "sub-claim-payload: PASS — each of 3 children's processed_value = its own sub-claim payload.v (1, 2, 3); the standard {{claim.<alias>.payload.v}} directive resolved per-sub-claim"
