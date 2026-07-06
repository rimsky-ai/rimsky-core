#!/usr/bin/env bash
# Copyright © 2026 Fall Guy Consulting.
# Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
# repo root, or http://www.apache.org/licenses/LICENSE-2.0.

# @story: cascade-send

set -euo pipefail

SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
TEMPLATE_PATH="${SCRIPT_DIR}/queue-drain-converges-demo-template.yaml"

RIMSKY_ENDPOINT="${RIMSKY_ENDPOINT:-http://127.0.0.1:8080}"

POLL_BUDGET_SECONDS="${POLL_BUDGET_SECONDS:-60}"

SELF_CHECK_LOG="$( mktemp -t queue-drain-converges-demo.XXXXXXXX )"
cleanup() {
    local rc=$?
    if [ "${rc}" -ne 0 ]; then
        echo "" >&2
        echo "queue-drain-converges-demo: captured observability log follows:" >&2
        cat "${SELF_CHECK_LOG}" >&2
    fi
    rm -f "${SELF_CHECK_LOG}"
    exit "${rc}"
}
trap cleanup EXIT

which yq >/dev/null 2>&1 || { echo "queue-drain-converges-demo: yq not on PATH" >&2; exit 2; }
which curl >/dev/null 2>&1 || { echo "queue-drain-converges-demo: curl not on PATH" >&2; exit 2; }
which jq >/dev/null 2>&1 || { echo "queue-drain-converges-demo: jq not on PATH" >&2; exit 2; }

echo "queue-drain-converges-demo: registering template at ${RIMSKY_ENDPOINT}"

SPEC_JSON="$( yq -o=json '.' "${TEMPLATE_PATH}" )"
REGISTER_BODY="$( jq -n --argjson spec "${SPEC_JSON}" '{spec: $spec}' )"
REGISTER_OUT="$( curl -sS -X POST -H 'Content-Type: application/json' \
    --data "${REGISTER_BODY}" \
    "${RIMSKY_ENDPOINT}/v1/templates" )"
TEMPLATE_HASH="$( echo "${REGISTER_OUT}" | jq -r '.template_id' )"
if [ -z "${TEMPLATE_HASH}" ] || [ "${TEMPLATE_HASH}" = "null" ]; then
    echo "queue-drain-converges-demo: template register failed: ${REGISTER_OUT}" >&2
    exit 1
fi
echo "queue-drain-converges-demo: template registered as ${TEMPLATE_HASH}"

DEPLOY_OUT="$( curl -sS -X POST -H 'Content-Type: application/json' \
    --data '{}' \
    "${RIMSKY_ENDPOINT}/v1/templates/${TEMPLATE_HASH}/deploy" )"
echo "queue-drain-converges-demo: template deployed: ${DEPLOY_OUT}"

INSTANCE_KEY="queue-drain-converges-demo-$( date +%s )-$$"
INSTANCE_OUT="$( curl -sS -X POST -H 'Content-Type: application/json' \
    --data "{\"template\": \"${TEMPLATE_HASH}\", \"instance_key\": \"${INSTANCE_KEY}\"}" \
    "${RIMSKY_ENDPOINT}/v1/instances" )"
INSTANCE_ID="$( echo "${INSTANCE_OUT}" | jq -r '.instance_id' )"
if [ -z "${INSTANCE_ID}" ] || [ "${INSTANCE_ID}" = "null" ]; then
    echo "queue-drain-converges-demo: instance create failed: ${INSTANCE_OUT}" >&2
    exit 1
fi
echo "queue-drain-converges-demo: instance ${INSTANCE_ID} created"

# @constraint: every emit must carry an Idempotency-Key (concept:message);
# a uuid-shaped value exercises the replay-dedup contract end-to-end.
IDEMPOTENCY_KEY="qdcd-wake-$( uuidgen 2>/dev/null || echo "${INSTANCE_KEY}" )"
MESSAGE_BODY='{"type":"loop/wake","payload":{"trip_counter":0}}'
MESSAGE_OUT="$( curl -sS -X POST -H 'Content-Type: application/json' \
    -H "Idempotency-Key: ${IDEMPOTENCY_KEY}" \
    --data "${MESSAGE_BODY}" \
    "${RIMSKY_ENDPOINT}/v1/instances/${INSTANCE_ID}/messages" )"
INITIAL_MESSAGE_ID="$( echo "${MESSAGE_OUT}" | jq -r '.message_id' )"
if [ -z "${INITIAL_MESSAGE_ID}" ] || [ "${INITIAL_MESSAGE_ID}" = "null" ]; then
    echo "queue-drain-converges-demo: initial message POST failed: ${MESSAGE_OUT}" >&2
    exit 1
fi
echo "queue-drain-converges-demo: posted initial wake message ${INITIAL_MESSAGE_ID}"

# @deliberate: poll the cascade-graph endpoint until BOTH the wake
# frame and the iterate frame appear, OR the budget runs out; capture
# polling output to stdout (for the human-facing demo) and to the
# self-check log (for the structural grep).
END=$(( $(date +%s) + POLL_BUDGET_SECONDS ))
SAW_WAKE=0
SAW_ITERATE=0
while [ "$( date +%s )" -lt "${END}" ]; do
    FRAMES_OUT="$( curl -sS \
        "${RIMSKY_ENDPOINT}/v1/instances/${INSTANCE_ID}/frames?limit=100" )"

    LINES="$( echo "${FRAMES_OUT}" | jq -r '.frames[] | "frame=" + .frame_id + " state=" + .state + " trigger=" + .triggering_message_id + " type=" + (.message_type // "<missing>")' )"
    # @constraint: reset the log on every poll so the final assertion
    # runs against the most recent snapshot — frames can change state
    # from running to ended between polls; we want the final view.
    echo "${LINES}" > "${SELF_CHECK_LOG}"
    echo "${LINES}"

    if echo "${LINES}" | grep -q 'type=loop/wake'; then
        SAW_WAKE=1
    fi
    if echo "${LINES}" | grep -q 'type=loop/iterate'; then
        SAW_ITERATE=1
    fi
    if [ "${SAW_WAKE}" -eq 1 ] && [ "${SAW_ITERATE}" -eq 1 ]; then
        # @deliberate: continue polling briefly so R settles into its
        # terminal state before the self-check fires.
        sleep 2
        FRAMES_OUT="$( curl -sS \
            "${RIMSKY_ENDPOINT}/v1/instances/${INSTANCE_ID}/frames?limit=100" )"
        LINES="$( echo "${FRAMES_OUT}" | jq -r '.frames[] | "frame=" + .frame_id + " state=" + .state + " trigger=" + .triggering_message_id + " type=" + (.message_type // "<missing>")' )"
        echo "${LINES}" > "${SELF_CHECK_LOG}"
        echo "${LINES}"
        break
    fi
    sleep 2
done

echo "queue-drain-converges-demo: running self-check on captured observability log"

# @constraint: every frame line must carry a non-empty
# triggering_message_id; a frame whose trigger field is empty
# (`trigger= ` with no UUID) violates the spec acceptance that every
# frame points back to the message ledger entry that triggered it.
TOTAL_FRAMES="$( grep -c '^frame=' "${SELF_CHECK_LOG}" || true )"
FRAMES_WITH_TRIGGER="$( grep -cE '^frame=[^ ]+ state=[^ ]+ trigger=[0-9a-fA-F-]{36} ' "${SELF_CHECK_LOG}" || true )"
if [ "${TOTAL_FRAMES}" -eq 0 ]; then
    echo "queue-drain-converges-demo: no frames observed in convergence window" >&2
    exit 1
fi
if [ "${TOTAL_FRAMES}" -ne "${FRAMES_WITH_TRIGGER}" ]; then
    echo "queue-drain-converges-demo: only ${FRAMES_WITH_TRIGGER}/${TOTAL_FRAMES} frame lines carry a triggering_message_id" >&2
    exit 1
fi

# @constraint: at least one frame must be the back-edge frame (the
# emit-node's loop/iterate message opened it); without this the
# cascade exhibited only the wake → A chain and the emit-node never
# fired — the "next frame opens carrying that message" leg failed.
if ! grep -q 'type=loop/iterate' "${SELF_CHECK_LOG}"; then
    echo "queue-drain-converges-demo: no frame opened by the loop/iterate back-edge message" >&2
    echo "queue-drain-converges-demo: the back-edge cascade did not fire (E never emitted)" >&2
    exit 1
fi

# @constraint: at least one frame must be the initial wake frame;
# without this the demo's initial POST never reached the cascade walker.
if ! grep -q 'type=loop/wake' "${SELF_CHECK_LOG}"; then
    echo "queue-drain-converges-demo: no frame opened by the loop/wake initial message" >&2
    echo "queue-drain-converges-demo: the wake → cascade leg failed" >&2
    exit 1
fi

echo "queue-drain-converges-demo: PASS — ${TOTAL_FRAMES} frames observed; every frame carries a triggering_message_id; both wake and back-edge messages opened frames"
