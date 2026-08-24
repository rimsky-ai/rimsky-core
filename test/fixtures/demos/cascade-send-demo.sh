#!/usr/bin/env bash
# Copyright © 2026 Fall Guy Consulting.
# SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

# @story: cascade-send

set -euo pipefail

SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
TEMPLATE_PATH="${SCRIPT_DIR}/cascade-send-demo-template.yaml"

RIMSKY_CONTROL_API_URL="${RIMSKY_CONTROL_API_URL:-http://127.0.0.1:8080}"

POLL_BUDGET_SECONDS="${POLL_BUDGET_SECONDS:-60}"

SELF_CHECK_LOG="$( mktemp -t cascade-send-demo.XXXXXXXX )"
cleanup() {
    local rc=$?
    if [ "${rc}" -ne 0 ]; then
        echo "" >&2
        echo "cascade-send-demo: captured observability log follows:" >&2
        cat "${SELF_CHECK_LOG}" >&2
    fi
    rm -f "${SELF_CHECK_LOG}"
    exit "${rc}"
}
trap cleanup EXIT

which yq >/dev/null 2>&1 || { echo "cascade-send-demo: yq not on PATH" >&2; exit 2; }
which curl >/dev/null 2>&1 || { echo "cascade-send-demo: curl not on PATH" >&2; exit 2; }
which jq >/dev/null 2>&1 || { echo "cascade-send-demo: jq not on PATH" >&2; exit 2; }

echo "cascade-send-demo: registering template at ${RIMSKY_CONTROL_API_URL}"

SPEC_JSON="$( yq -o=json '.' "${TEMPLATE_PATH}" )"
REGISTER_BODY="$( jq -n --argjson spec "${SPEC_JSON}" '{spec: $spec}' )"
REGISTER_OUT="$( curl -sS -X POST -H 'Content-Type: application/json' \
    --data "${REGISTER_BODY}" \
    "${RIMSKY_CONTROL_API_URL}/v1/templates" )"
TEMPLATE_HASH="$( echo "${REGISTER_OUT}" | jq -r '.template_id' )"
if [ -z "${TEMPLATE_HASH}" ] || [ "${TEMPLATE_HASH}" = "null" ]; then
    echo "cascade-send-demo: template register failed: ${REGISTER_OUT}" >&2
    exit 1
fi
echo "cascade-send-demo: template registered as ${TEMPLATE_HASH}"

DEPLOY_OUT="$( curl -sS -X POST -H 'Content-Type: application/json' \
    --data '{}' \
    "${RIMSKY_CONTROL_API_URL}/v1/templates/${TEMPLATE_HASH}/deploy" )"
echo "cascade-send-demo: template deployed: ${DEPLOY_OUT}"

INSTANCE_KEY="cascade-send-demo-$( date +%s )-$$"
INSTANCE_OUT="$( curl -sS -X POST -H 'Content-Type: application/json' \
    --data "{\"template\": \"${TEMPLATE_HASH}\", \"instance_key\": \"${INSTANCE_KEY}\", \"target_daemon\": \"demo-daemon\"}" \
    "${RIMSKY_CONTROL_API_URL}/v1/instances" )"
INSTANCE_ID="$( echo "${INSTANCE_OUT}" | jq -r '.instance_id' )"
if [ -z "${INSTANCE_ID}" ] || [ "${INSTANCE_ID}" = "null" ]; then
    echo "cascade-send-demo: instance create failed: ${INSTANCE_OUT}" >&2
    exit 1
fi
echo "cascade-send-demo: instance ${INSTANCE_ID} created"

# @concept: message
IDEMPOTENCY_KEY="csd-wake-$( uuidgen 2>/dev/null || echo "${INSTANCE_KEY}" )"
MESSAGE_BODY='{"type":"loop/wake","payload":{"trip_counter":0}}'
MESSAGE_OUT="$( curl -sS -X POST -H 'Content-Type: application/json' \
    -H "Idempotency-Key: ${IDEMPOTENCY_KEY}" \
    --data "${MESSAGE_BODY}" \
    "${RIMSKY_CONTROL_API_URL}/v1/instances/${INSTANCE_ID}/messages" )"
INITIAL_MESSAGE_ID="$( echo "${MESSAGE_OUT}" | jq -r '.message_id' )"
if [ -z "${INITIAL_MESSAGE_ID}" ] || [ "${INITIAL_MESSAGE_ID}" = "null" ]; then
    echo "cascade-send-demo: initial message POST failed: ${MESSAGE_OUT}" >&2
    exit 1
fi
echo "cascade-send-demo: posted initial wake message ${INITIAL_MESSAGE_ID}"

END=$(( $(date +%s) + POLL_BUDGET_SECONDS ))
SAW_WAKE=0
SAW_ITERATE=0
while [ "$( date +%s )" -lt "${END}" ]; do
    FRAMES_OUT="$( curl -sS \
        "${RIMSKY_CONTROL_API_URL}/v1/instances/${INSTANCE_ID}/frames?limit=100" )"

    LINES="$( echo "${FRAMES_OUT}" | jq -r '.frames[] | "frame=" + .frame_id + " state=" + .state + " trigger=" + .triggering_message_id + " type=" + (.message_type // "<missing>")' )"
    echo "${LINES}" > "${SELF_CHECK_LOG}"
    echo "${LINES}"

    if echo "${LINES}" | grep -q 'type=loop/wake'; then
        SAW_WAKE=1
    fi
    if echo "${LINES}" | grep -q 'type=loop/iterate'; then
        SAW_ITERATE=1
    fi
    if [ "${SAW_WAKE}" -eq 1 ] && [ "${SAW_ITERATE}" -eq 1 ]; then
        sleep 2
        FRAMES_OUT="$( curl -sS \
            "${RIMSKY_CONTROL_API_URL}/v1/instances/${INSTANCE_ID}/frames?limit=100" )"
        LINES="$( echo "${FRAMES_OUT}" | jq -r '.frames[] | "frame=" + .frame_id + " state=" + .state + " trigger=" + .triggering_message_id + " type=" + (.message_type // "<missing>")' )"
        echo "${LINES}" > "${SELF_CHECK_LOG}"
        echo "${LINES}"
        break
    fi
    sleep 2
done

echo "cascade-send-demo: running self-check on captured observability log"

TOTAL_FRAMES="$( grep -c '^frame=' "${SELF_CHECK_LOG}" || true )"
FRAMES_WITH_TRIGGER="$( grep -cE '^frame=[^ ]+ state=[^ ]+ trigger=[0-9a-fA-F-]{36} ' "${SELF_CHECK_LOG}" || true )"
if [ "${TOTAL_FRAMES}" -eq 0 ]; then
    echo "cascade-send-demo: no frames observed in convergence window" >&2
    exit 1
fi
if [ "${TOTAL_FRAMES}" -ne "${FRAMES_WITH_TRIGGER}" ]; then
    echo "cascade-send-demo: only ${FRAMES_WITH_TRIGGER}/${TOTAL_FRAMES} frame lines carry a triggering_message_id" >&2
    exit 1
fi

if ! grep -q 'type=loop/iterate' "${SELF_CHECK_LOG}"; then
    echo "cascade-send-demo: no frame opened by the loop/iterate back-edge message" >&2
    echo "cascade-send-demo: the back-edge cascade did not fire (E never sent)" >&2
    exit 1
fi

if ! grep -q 'type=loop/wake' "${SELF_CHECK_LOG}"; then
    echo "cascade-send-demo: no frame opened by the loop/wake initial message" >&2
    echo "cascade-send-demo: the wake → cascade leg failed" >&2
    exit 1
fi

echo "cascade-send-demo: PASS — ${TOTAL_FRAMES} frames observed; every frame carries a triggering_message_id; both wake and back-edge messages opened frames"
