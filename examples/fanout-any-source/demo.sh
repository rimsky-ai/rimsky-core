#!/usr/bin/env bash
# Copyright © 2026 Fall Guy Consulting.
# Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
# repo root, or http://www.apache.org/licenses/LICENSE-2.0.

# @story: fanout-any-substitution-source

set -euo pipefail

SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
TEMPLATE_FROM_NODE="${SCRIPT_DIR}/template-from-node.yaml"
TEMPLATE_FROM_MESSAGE="${SCRIPT_DIR}/template-from-message.yaml"

RIMSKY_CONTROL_API_URL="${RIMSKY_CONTROL_API_URL:-http://127.0.0.1:8080}"
POLL_BUDGET_SECONDS="${POLL_BUDGET_SECONDS:-120}"

which yq >/dev/null 2>&1 || { echo "fanout-any-source: yq not on PATH" >&2; exit 2; }
which curl >/dev/null 2>&1 || { echo "fanout-any-source: curl not on PATH" >&2; exit 2; }
which jq >/dev/null 2>&1 || { echo "fanout-any-source: jq not on PATH" >&2; exit 2; }

register_and_deploy() {
    local path="$1"
    local label="$2"
    local spec_json
    spec_json="$( yq -o=json '.' "${path}" )"
    local body
    body="$( jq -n --argjson spec "${spec_json}" '{spec: $spec}' )"
    local out
    out="$( curl -sS -X POST -H 'Content-Type: application/json' \
        --data "${body}" \
        "${RIMSKY_CONTROL_API_URL}/v1/templates" )"
    local tid
    tid="$( echo "${out}" | jq -r '.template_id' )"
    if [ -z "${tid}" ] || [ "${tid}" = "null" ]; then
        echo "fanout-any-source: register ${label} failed: ${out}" >&2
        return 1
    fi
    echo "fanout-any-source: registered ${label} as ${tid}"
    curl -sS -X POST -H 'Content-Type: application/json' --data '{}' \
        "${RIMSKY_CONTROL_API_URL}/v1/templates/${tid}/deploy" >/dev/null
    echo "${tid}"
}

create_instance() {
    local tid="$1"
    local key="$2"
    local out
    out="$( curl -sS -X POST -H 'Content-Type: application/json' \
        --data "{\"template\": \"${tid}\", \"instance_key\": \"${key}\", \"target_agent\": \"demo-agent\"}" \
        "${RIMSKY_CONTROL_API_URL}/v1/instances" )"
    local iid
    iid="$( echo "${out}" | jq -r '.instance_id' )"
    if [ -z "${iid}" ] || [ "${iid}" = "null" ]; then
        echo "fanout-any-source: instance create failed: ${out}" >&2
        return 1
    fi
    echo "${iid}"
}

post_message() {
    local iid="$1"
    local body="$2"
    local k
    k="fas-$( uuidgen 2>/dev/null || echo "${iid}-${RANDOM}" )"
    curl -sS -X POST -H 'Content-Type: application/json' \
        -H "Idempotency-Key: ${k}" \
        --data "${body}" \
        "${RIMSKY_CONTROL_API_URL}/v1/instances/${iid}/messages" >/dev/null
}

count_distinct_partition_keys() {
    local iid="$1"
    local node_type="$2"
    local nodes_out
    nodes_out="$( curl -sS "${RIMSKY_CONTROL_API_URL}/v1/instances/${iid}/nodes?limit=100" )"
    local ids
    ids="$( echo "${nodes_out}" \
        | jq -r --arg t "${node_type}" '.nodes[] | select(.node_type == $t) | .id' )"
    if [ -z "${ids}" ]; then
        echo "0"
        return
    fi
    local keys=()
    while IFS= read -r nid; do
        [ -z "${nid}" ] && continue
        local detail
        detail="$( curl -sS "${RIMSKY_CONTROL_API_URL}/v1/nodes/${nid}" )"
        local pk
        pk="$( echo "${detail}" | jq -r '.latest_attributes.partition_key // empty' )"
        if [ -n "${pk}" ]; then
            keys+=("${pk}")
        fi
    done <<< "${ids}"
    printf '%s\n' "${keys[@]:-}" | sort -u | grep -v '^$' | wc -l | tr -d ' '
}

echo "fanout-any-source: registering both templates"
NODE_TID="$( register_and_deploy "${TEMPLATE_FROM_NODE}" "from-node" )"
MSG_TID="$( register_and_deploy "${TEMPLATE_FROM_MESSAGE}" "from-message" )"

NODE_KEY="fas-from-node-$( date +%s )-$$"
MSG_KEY="fas-from-message-$( date +%s )-$$"

NODE_IID="$( create_instance "${NODE_TID}" "${NODE_KEY}" )"
MSG_IID="$( create_instance "${MSG_TID}" "${MSG_KEY}" )"
echo "fanout-any-source: node-sourced instance ${NODE_IID}; message-sourced instance ${MSG_IID}"

post_message "${NODE_IID}" '{}'

post_message "${MSG_IID}" '{"type":"backfill_trigger","payload":{"items":[{"key":"x","payload":{}},{"key":"y","payload":{}}]}}'

END=$(( $(date +%s) + POLL_BUDGET_SECONDS ))
NODE_OK=0
MSG_OK=0
while [ "$( date +%s )" -lt "${END}" ]; do
    if [ "${NODE_OK}" -eq 0 ]; then
        NODE_COUNT="$( count_distinct_partition_keys "${NODE_IID}" triage )"
        if [ "${NODE_COUNT}" -ge 3 ]; then
            NODE_OK=1
            echo "fanout-any-source: node-sourced instance dispatched ${NODE_COUNT} children (expected 3)"
        fi
    fi
    if [ "${MSG_OK}" -eq 0 ]; then
        MSG_COUNT="$( count_distinct_partition_keys "${MSG_IID}" triage )"
        if [ "${MSG_COUNT}" -ge 2 ]; then
            MSG_OK=1
            echo "fanout-any-source: message-sourced instance dispatched ${MSG_COUNT} children (expected 2)"
        fi
    fi
    if [ "${NODE_OK}" -eq 1 ] && [ "${MSG_OK}" -eq 1 ]; then
        break
    fi
    sleep 2
done

if [ "${NODE_OK}" -eq 0 ]; then
    echo "fanout-any-source: FAIL — node-sourced instance did not dispatch 3 children within ${POLL_BUDGET_SECONDS}s" >&2
    exit 1
fi
if [ "${MSG_OK}" -eq 0 ]; then
    echo "fanout-any-source: FAIL — message-sourced instance did not dispatch 2 children within ${POLL_BUDGET_SECONDS}s" >&2
    exit 1
fi

echo "fanout-any-source: PASS — partition_request resolves uniformly from both upstream-node-attribute and typed-message sources"
