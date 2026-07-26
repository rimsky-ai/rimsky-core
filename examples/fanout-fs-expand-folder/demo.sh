#!/usr/bin/env bash
# Copyright © 2026 Fall Guy Consulting.
# Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
# repo root, or http://www.apache.org/licenses/LICENSE-2.0.

# @story: fs-fanout-expand-folder

set -euo pipefail

SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
TEMPLATE_PATH="${SCRIPT_DIR}/template.yaml"

RIMSKY_CONTROL_API_URL="${RIMSKY_CONTROL_API_URL:-http://127.0.0.1:8080}"
POLL_BUDGET_SECONDS="${POLL_BUDGET_SECONDS:-120}"

which yq >/dev/null 2>&1 || { echo "fanout-fs-expand-folder: yq not on PATH" >&2; exit 2; }
which curl >/dev/null 2>&1 || { echo "fanout-fs-expand-folder: curl not on PATH" >&2; exit 2; }
which jq >/dev/null 2>&1 || { echo "fanout-fs-expand-folder: jq not on PATH" >&2; exit 2; }

echo "fanout-fs-expand-folder: registering template at ${RIMSKY_CONTROL_API_URL}"

SPEC_JSON="$( yq -o=json '.' "${TEMPLATE_PATH}" )"
REGISTER_BODY="$( jq -n --argjson spec "${SPEC_JSON}" '{spec: $spec}' )"
REGISTER_OUT="$( curl -sS -X POST -H 'Content-Type: application/json' \
    --data "${REGISTER_BODY}" \
    "${RIMSKY_CONTROL_API_URL}/v1/templates" )"
TEMPLATE_ID="$( echo "${REGISTER_OUT}" | jq -r '.template_id' )"
if [ -z "${TEMPLATE_ID}" ] || [ "${TEMPLATE_ID}" = "null" ]; then
    echo "fanout-fs-expand-folder: template register failed: ${REGISTER_OUT}" >&2
    exit 1
fi
echo "fanout-fs-expand-folder: registered template ${TEMPLATE_ID}"

DEPLOY_OUT="$( curl -sS -X POST -H 'Content-Type: application/json' \
    --data '{}' \
    "${RIMSKY_CONTROL_API_URL}/v1/templates/${TEMPLATE_ID}/deploy" )"
echo "fanout-fs-expand-folder: template deployed: ${DEPLOY_OUT}"

INSTANCE_KEY="fanout-fs-expand-folder-$( date +%s )-$$"
INSTANCE_OUT="$( curl -sS -X POST -H 'Content-Type: application/json' \
    --data "{\"template\": \"${TEMPLATE_ID}\", \"instance_key\": \"${INSTANCE_KEY}\", \"target_agent\": \"demo-agent\"}" \
    "${RIMSKY_CONTROL_API_URL}/v1/instances" )"
INSTANCE_ID="$( echo "${INSTANCE_OUT}" | jq -r '.instance_id' )"
if [ -z "${INSTANCE_ID}" ] || [ "${INSTANCE_ID}" = "null" ]; then
    echo "fanout-fs-expand-folder: instance create failed: ${INSTANCE_OUT}" >&2
    exit 1
fi
echo "fanout-fs-expand-folder: instance ${INSTANCE_ID} created"

IDEMPOTENCY_KEY="ffef-$( uuidgen 2>/dev/null || echo "${INSTANCE_KEY}" )"
MESSAGE_BODY='{"type":"wake","payload":{"nonce":"go"}}'
MESSAGE_OUT="$( curl -sS -X POST -H 'Content-Type: application/json' \
    -H "Idempotency-Key: ${IDEMPOTENCY_KEY}" \
    --data "${MESSAGE_BODY}" \
    "${RIMSKY_CONTROL_API_URL}/v1/instances/${INSTANCE_ID}/messages" )"
MESSAGE_ID="$( echo "${MESSAGE_OUT}" | jq -r '.message_id' )"
if [ -z "${MESSAGE_ID}" ] || [ "${MESSAGE_ID}" = "null" ]; then
    echo "fanout-fs-expand-folder: wake POST failed: ${MESSAGE_OUT}" >&2
    exit 1
fi
echo "fanout-fs-expand-folder: posted wake message ${MESSAGE_ID}"

END=$(( $(date +%s) + POLL_BUDGET_SECONDS ))
SAW_THREE_FILES=0
LAST_NODES_OUT=""
while [ "$( date +%s )" -lt "${END}" ]; do
    NODES_OUT="$( curl -sS "${RIMSKY_CONTROL_API_URL}/v1/instances/${INSTANCE_ID}/nodes?limit=100" )"
    LAST_NODES_OUT="${NODES_OUT}"
    CHILD_IDS="$( echo "${NODES_OUT}" \
        | jq -r '.nodes[] | select(.node_type == "process_files") | .id' )"
    if [ -z "${CHILD_IDS}" ]; then
        sleep 2
        continue
    fi
    ADDRESSES=()
    while IFS= read -r NID; do
        [ -z "${NID}" ] && continue
        DETAIL="$( curl -sS "${RIMSKY_CONTROL_API_URL}/v1/nodes/${NID}" )"
        ADDR="$( echo "${DETAIL}" | jq -r '.latest_attributes.file_address // empty' )"
        if [ -n "${ADDR}" ]; then
            ADDRESSES+=("${ADDR}")
        fi
    done <<< "${CHILD_IDS}"

    DISTINCT="$( printf '%s\n' "${ADDRESSES[@]:-}" | sort -u | grep -v '^$' | wc -l | tr -d ' ' )"
    if [ "${DISTINCT}" -ge 3 ]; then
        SAW_A=0; SAW_B=0; SAW_C=0
        for A in "${ADDRESSES[@]}"; do
            case "${A}" in
                *a.json) SAW_A=1 ;;
                *b.json) SAW_B=1 ;;
                *c.json) SAW_C=1 ;;
            esac
        done
        if [ "${SAW_A}" -eq 1 ] && [ "${SAW_B}" -eq 1 ] && [ "${SAW_C}" -eq 1 ]; then
            SAW_THREE_FILES=1
            break
        fi
    fi
    sleep 2
done

if [ "${SAW_THREE_FILES}" -eq 0 ]; then
    echo "fanout-fs-expand-folder: FAIL — did not observe 3 children addressing a.json / b.json / c.json within ${POLL_BUDGET_SECONDS}s" >&2
    echo "${LAST_NODES_OUT}" | jq '.' >&2 || true
    exit 1
fi

echo "fanout-fs-expand-folder: PASS — 3 children dispatched, each addressing its own seeded file path"
