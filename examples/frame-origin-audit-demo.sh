#!/usr/bin/env bash
# Copyright © 2026 Fall Guy Consulting.
# Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
# repo root, or http://www.apache.org/licenses/LICENSE-2.0.

# @story: frame-origin-audit
set -euo pipefail

SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
TEMPLATE_PATH="${SCRIPT_DIR}/frame-origin-audit-demo-template.yaml"

RIMSKY_CONTROL_API_URL="${RIMSKY_CONTROL_API_URL:-http://127.0.0.1:8080}"
POLL_BUDGET_SECONDS="${POLL_BUDGET_SECONDS:-60}"

SELF_CHECK_LOG="$( mktemp -t frame-origin-audit-demo.XXXXXXXX )"
cleanup() {
    local rc=$?
    if [ "${rc}" -ne 0 ]; then
        echo "" >&2
        echo "frame-origin-audit-demo: captured observability log follows:" >&2
        cat "${SELF_CHECK_LOG}" >&2
    fi
    rm -f "${SELF_CHECK_LOG}"
    exit "${rc}"
}
trap cleanup EXIT

which yq >/dev/null 2>&1 || { echo "frame-origin-audit-demo: yq not on PATH" >&2; exit 2; }
which curl >/dev/null 2>&1 || { echo "frame-origin-audit-demo: curl not on PATH" >&2; exit 2; }
which jq >/dev/null 2>&1 || { echo "frame-origin-audit-demo: jq not on PATH" >&2; exit 2; }

echo "frame-origin-audit-demo: registering template at ${RIMSKY_CONTROL_API_URL}"

SPEC_JSON="$( yq -o=json '.' "${TEMPLATE_PATH}" )"
REGISTER_BODY="$( jq -n --argjson spec "${SPEC_JSON}" '{spec: $spec}' )"
REGISTER_OUT="$( curl -sS -X POST -H 'Content-Type: application/json' \
    --data "${REGISTER_BODY}" \
    "${RIMSKY_CONTROL_API_URL}/v1/templates" )"
TEMPLATE_HASH="$( echo "${REGISTER_OUT}" | jq -r '.template_id' )"
if [ -z "${TEMPLATE_HASH}" ] || [ "${TEMPLATE_HASH}" = "null" ]; then
    echo "frame-origin-audit-demo: template register failed: ${REGISTER_OUT}" >&2
    exit 1
fi
echo "frame-origin-audit-demo: template registered as ${TEMPLATE_HASH}"

DEPLOY_OUT="$( curl -sS -X POST -H 'Content-Type: application/json' \
    --data '{}' \
    "${RIMSKY_CONTROL_API_URL}/v1/templates/${TEMPLATE_HASH}/deploy" )"
echo "frame-origin-audit-demo: template deployed: ${DEPLOY_OUT}"

INSTANCE_KEY="frame-origin-audit-demo-$( date +%s )-$$"
INSTANCE_OUT="$( curl -sS -X POST -H 'Content-Type: application/json' \
    --data "{\"template\": \"${TEMPLATE_HASH}\", \"instance_key\": \"${INSTANCE_KEY}\", \"target_agent\": \"demo-agent\"}" \
    "${RIMSKY_CONTROL_API_URL}/v1/instances" )"
INSTANCE_ID="$( echo "${INSTANCE_OUT}" | jq -r '.instance_id' )"
if [ -z "${INSTANCE_ID}" ] || [ "${INSTANCE_ID}" = "null" ]; then
    echo "frame-origin-audit-demo: instance create failed: ${INSTANCE_OUT}" >&2
    exit 1
fi
echo "frame-origin-audit-demo: instance ${INSTANCE_ID} created"

IDEMPOTENCY_KEY="audit-kick-$( uuidgen 2>/dev/null || echo "${INSTANCE_KEY}" )"
MESSAGE_OUT="$( curl -sS -X POST -H 'Content-Type: application/json' \
    -H "Idempotency-Key: ${IDEMPOTENCY_KEY}" \
    --data '{"type":"audit/kick","payload":{"kick":"go"}}' \
    "${RIMSKY_CONTROL_API_URL}/v1/instances/${INSTANCE_ID}/messages" )"
INITIAL_MESSAGE_ID="$( echo "${MESSAGE_OUT}" | jq -r '.message_id' )"
if [ -z "${INITIAL_MESSAGE_ID}" ] || [ "${INITIAL_MESSAGE_ID}" = "null" ]; then
    echo "frame-origin-audit-demo: initial message POST failed: ${MESSAGE_OUT}" >&2
    exit 1
fi
echo "frame-origin-audit-demo: posted kick message ${INITIAL_MESSAGE_ID}"

END=$(( $(date +%s) + POLL_BUDGET_SECONDS ))
SAW_OPERATOR=0
SAW_INSTANCE=0
while [ "$( date +%s )" -lt "${END}" ]; do
    FRAMES_OUT="$( curl -sS \
        "${RIMSKY_CONTROL_API_URL}/v1/instances/${INSTANCE_ID}/frames?limit=100" )"

    LINES="$( echo "${FRAMES_OUT}" | jq -r '.frames[] |
        "frame=" + .frame_id +
        " trigger=" + .triggering_message_id +
        " type=" + (.message_type // "<missing>") +
        " sender=" + (.message_sender // "<missing>") +
        " kind=" + (.message_sender_kind // "<missing>")' )"
    echo "${LINES}" > "${SELF_CHECK_LOG}"
    echo "${LINES}"

    if echo "${LINES}" | grep -q ' kind=operator'; then
        SAW_OPERATOR=1
    fi
    if echo "${LINES}" | grep -q ' kind=instance'; then
        SAW_INSTANCE=1
    fi
    if [ "${SAW_OPERATOR}" -eq 1 ] && [ "${SAW_INSTANCE}" -eq 1 ]; then
        sleep 2
        FRAMES_OUT="$( curl -sS \
            "${RIMSKY_CONTROL_API_URL}/v1/instances/${INSTANCE_ID}/frames?limit=100" )"
        LINES="$( echo "${FRAMES_OUT}" | jq -r '.frames[] |
            "frame=" + .frame_id +
            " trigger=" + .triggering_message_id +
            " type=" + (.message_type // "<missing>") +
            " sender=" + (.message_sender // "<missing>") +
            " kind=" + (.message_sender_kind // "<missing>")' )"
        echo "${LINES}" > "${SELF_CHECK_LOG}"
        echo "${LINES}"
        break
    fi
    sleep 2
done

# @story: frame-origin-audit
echo "frame-origin-audit-demo: running self-check on captured observability log"

TOTAL_FRAMES="$( grep -c '^frame=' "${SELF_CHECK_LOG}" || true )"
if [ "${TOTAL_FRAMES}" -eq 0 ]; then
    echo "frame-origin-audit-demo: no frames observed in convergence window" >&2
    exit 1
fi

FRAMES_WITH_TRIGGER="$( grep -cE '^frame=[0-9a-fA-F-]{36} trigger=[0-9a-fA-F-]{36} ' "${SELF_CHECK_LOG}" || true )"
if [ "${TOTAL_FRAMES}" -ne "${FRAMES_WITH_TRIGGER}" ]; then
    echo "frame-origin-audit-demo: only ${FRAMES_WITH_TRIGGER}/${TOTAL_FRAMES} frame lines carry a triggering_message_id" >&2
    exit 1
fi

if grep -q 'type=<missing>' "${SELF_CHECK_LOG}"; then
    echo "frame-origin-audit-demo: at least one frame has no joined message type" >&2
    exit 1
fi
if grep -q 'sender=<missing>' "${SELF_CHECK_LOG}"; then
    echo "frame-origin-audit-demo: at least one frame has no joined message sender" >&2
    exit 1
fi

if ! grep -q ' kind=operator' "${SELF_CHECK_LOG}"; then
    echo "frame-origin-audit-demo: no operator-origin frame observed" >&2
    exit 1
fi
if ! grep -q ' kind=instance' "${SELF_CHECK_LOG}"; then
    echo "frame-origin-audit-demo: no cascade-send (sender_kind=instance) origin frame observed" >&2
    echo "frame-origin-audit-demo: the send-node back-edge did not fire" >&2
    exit 1
fi

echo "frame-origin-audit-demo: PASS — ${TOTAL_FRAMES} frames observed; every frame carries a triggering_message_id, joined envelope type, and joined sender"
