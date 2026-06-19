#!/usr/bin/env bash
# Copyright © 2026 Fall Guy Consulting.
# Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
# repo root, or http://www.apache.org/licenses/LICENSE-2.0.

# @story: messages-as-nodes-substitution

set -euo pipefail

SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
TEMPLATE_VALID="${SCRIPT_DIR}/template-valid.yaml"
TEMPLATE_UNDECLARED="${SCRIPT_DIR}/template-undeclared.yaml"

RIMSKY_ENDPOINT="${RIMSKY_ENDPOINT:-http://127.0.0.1:8080}"
POLL_BUDGET_SECONDS="${POLL_BUDGET_SECONDS:-90}"

which yq >/dev/null 2>&1 || { echo "messages-as-nodes: yq not on PATH" >&2; exit 2; }
which curl >/dev/null 2>&1 || { echo "messages-as-nodes: curl not on PATH" >&2; exit 2; }
which jq >/dev/null 2>&1 || { echo "messages-as-nodes: jq not on PATH" >&2; exit 2; }

echo "messages-as-nodes: registering valid template"
SPEC_JSON_VALID="$( yq -o=json '.' "${TEMPLATE_VALID}" )"
BODY_VALID="$( jq -n --argjson spec "${SPEC_JSON_VALID}" '{spec: $spec}' )"
REGISTER_VALID="$( curl -sS -X POST -H 'Content-Type: application/json' \
    --data "${BODY_VALID}" \
    "${RIMSKY_ENDPOINT}/v1/templates" )"
VALID_TID="$( echo "${REGISTER_VALID}" | jq -r '.template_id' )"
if [ -z "${VALID_TID}" ] || [ "${VALID_TID}" = "null" ]; then
    echo "messages-as-nodes: valid template register failed: ${REGISTER_VALID}" >&2
    exit 1
fi
echo "messages-as-nodes: valid template registered as ${VALID_TID}"

echo "messages-as-nodes: attempting to register undeclared template (must fail)"
SPEC_JSON_BAD="$( yq -o=json '.' "${TEMPLATE_UNDECLARED}" )"
BODY_BAD="$( jq -n --argjson spec "${SPEC_JSON_BAD}" '{spec: $spec}' )"
REGISTER_BAD="$( curl -sS -o /dev/stdout -w '\n%{http_code}' \
    -X POST -H 'Content-Type: application/json' \
    --data "${BODY_BAD}" \
    "${RIMSKY_ENDPOINT}/v1/templates" )"
BAD_HTTP_CODE="$( echo "${REGISTER_BAD}" | tail -n1 )"
BAD_BODY="$( echo "${REGISTER_BAD}" | sed '$d' )"
if [ "${BAD_HTTP_CODE}" = "200" ] || [ "${BAD_HTTP_CODE}" = "201" ]; then
    echo "messages-as-nodes: FAIL — undeclared template registered with HTTP ${BAD_HTTP_CODE} (should have rejected)" >&2
    echo "${BAD_BODY}" >&2
    exit 1
fi
if ! echo "${BAD_BODY}" | grep -q 'bar'; then
    echo "messages-as-nodes: FAIL — rejection body does not mention the undeclared type 'bar'" >&2
    echo "${BAD_BODY}" >&2
    exit 1
fi
if ! echo "${BAD_BODY}" | grep -qiE 'messages:|messages registry'; then
    echo "messages-as-nodes: FAIL — rejection body does not mention 'messages:' or the missing registry declaration" >&2
    echo "${BAD_BODY}" >&2
    exit 1
fi
echo "messages-as-nodes: undeclared template rejected (HTTP ${BAD_HTTP_CODE}) — error mentions 'bar' and 'messages:'"

curl -sS -X POST -H 'Content-Type: application/json' --data '{}' \
    "${RIMSKY_ENDPOINT}/v1/templates/${VALID_TID}/deploy" >/dev/null
echo "messages-as-nodes: valid template deployed"

INSTANCE_KEY="man-$( date +%s )-$$"
INSTANCE_OUT="$( curl -sS -X POST -H 'Content-Type: application/json' \
    --data "{\"template\": \"${VALID_TID}\", \"instance_key\": \"${INSTANCE_KEY}\"}" \
    "${RIMSKY_ENDPOINT}/v1/instances" )"
INSTANCE_ID="$( echo "${INSTANCE_OUT}" | jq -r '.instance_id' )"
if [ -z "${INSTANCE_ID}" ] || [ "${INSTANCE_ID}" = "null" ]; then
    echo "messages-as-nodes: instance create failed: ${INSTANCE_OUT}" >&2
    exit 1
fi
echo "messages-as-nodes: instance ${INSTANCE_ID} created"

K1="man-wake-$( uuidgen 2>/dev/null || echo "${INSTANCE_KEY}" )"
curl -sS -X POST -H 'Content-Type: application/json' \
    -H "Idempotency-Key: ${K1}" \
    --data '{}' \
    "${RIMSKY_ENDPOINT}/v1/instances/${INSTANCE_ID}/messages" >/dev/null

K2="man-foo-$( uuidgen 2>/dev/null || echo "${INSTANCE_KEY}" )"
curl -sS -X POST -H 'Content-Type: application/json' \
    -H "Idempotency-Key: ${K2}" \
    --data '{"type":"foo","payload":{"body":"from-message"}}' \
    "${RIMSKY_ENDPOINT}/v1/instances/${INSTANCE_ID}/messages" >/dev/null

# Poll the `receiver` node until its latest_attributes carry the two
# substituted values: from_message="from-message" and from_node="from-node".
END=$(( $(date +%s) + POLL_BUDGET_SECONDS ))
SAW_BOTH=0
LAST_DETAIL=""
while [ "$( date +%s )" -lt "${END}" ]; do
    NODES_OUT="$( curl -sS "${RIMSKY_ENDPOINT}/v1/instances/${INSTANCE_ID}/nodes?limit=100" )"
    RECV_ID="$( echo "${NODES_OUT}" | jq -r '.nodes[] | select(.node_type == "receiver") | .id' | head -n1 )"
    if [ -z "${RECV_ID}" ]; then
        sleep 2
        continue
    fi
    DETAIL="$( curl -sS "${RIMSKY_ENDPOINT}/v1/nodes/${RECV_ID}" )"
    LAST_DETAIL="${DETAIL}"
    FROM_MSG="$( echo "${DETAIL}" | jq -r '.latest_attributes.from_message // empty' )"
    FROM_NODE="$( echo "${DETAIL}" | jq -r '.latest_attributes.from_node // empty' )"
    if [ "${FROM_MSG}" = "from-message" ] && [ "${FROM_NODE}" = "from-node" ]; then
        SAW_BOTH=1
        break
    fi
    sleep 2
done

if [ "${SAW_BOTH}" -eq 0 ]; then
    echo "messages-as-nodes: FAIL — receiver did not see both substituted values within ${POLL_BUDGET_SECONDS}s" >&2
    echo "${LAST_DETAIL}" | jq '.latest_attributes // .' >&2 || true
    exit 1
fi

echo "messages-as-nodes: PASS — both {{messages.foo.body}} and {{nodes.bar.attribute.body}} resolved through the unified lookup; undeclared message-type rejected at registration"
