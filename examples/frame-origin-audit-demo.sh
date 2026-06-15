#!/usr/bin/env bash
# Copyright © 2026 Fall Guy Consulting.
# Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
# repo root, or http://www.apache.org/licenses/LICENSE-2.0.

# frame-origin-audit-demo.sh — STORY-frame-origin-audit proof.
#
# An operator answers "why did this frame open" for every frame the
# cascade-graph observability endpoint surfaces. The spec's acceptance
# is "every frame carries a pointer back to the message ledger entry
# that triggered it." The demo script:
#
#   1. Boots / connects to a rimsky stack.
#   2. Registers + deploys the demo template (a two-frame bounded
#      back-edge so both origins — operator-posted, cascade-emitted —
#      are exhibited).
#   3. Creates an instance.
#   4. Posts the initial kick message.
#   5. Polls GET /v1/instances/{id}/frames until both frames appear.
#   6. For each frame, prints frame_id, triggering_message_id, and the
#      joined envelope's type + sender (the observability surface the
#      spec calls out: "looking up a frame returns the originating
#      message envelope (sender, type, body)").
#   7. Self-checks: every frame line MUST carry a non-empty
#      triggering_message_id, non-empty joined sender + type, and at
#      least one operator-origin frame plus at least one cascade-emit
#      (sender_kind=instance) origin frame. The script exits non-zero
#      if any requirement is missing.
#
# Prerequisites the operator must satisfy BEFORE running this script:
#
#   1. A running rimsky stack reachable at RIMSKY_ENDPOINT
#      (default http://127.0.0.1:8080).
#   2. The bundled `verifier-shape-checks` executor declared in
#      rimsky.yml under `executors:`. The services-scenarios driver
#      test (lib/services/test/scenarios/frame_origin_audit_demo_e2e_test.go)
#      wires this automatically via testcontainers. For a bare-metal
#      stack, see examples/README.md for the wiring.
#   3. `curl`, `jq`, and `yq` on $PATH.
#
# Output discipline: exits 0 only when EVERY frame line carries a
# non-empty triggering_message_id, EVERY frame has a joined sender +
# type populated (not the LEFT JOIN fallback's empty string), AND at
# least one frame is a cascade-emit (sender_kind=instance) — so the
# audit surface is exercised for both operator-posted and
# cascade-emitted frame origins.

set -euo pipefail

SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
TEMPLATE_PATH="${SCRIPT_DIR}/frame-origin-audit-demo-template.yaml"

RIMSKY_ENDPOINT="${RIMSKY_ENDPOINT:-http://127.0.0.1:8080}"
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

echo "frame-origin-audit-demo: registering template at ${RIMSKY_ENDPOINT}"

SPEC_JSON="$( yq -o=json '.' "${TEMPLATE_PATH}" )"
REGISTER_BODY="$( jq -n --argjson spec "${SPEC_JSON}" '{spec: $spec}' )"
REGISTER_OUT="$( curl -sS -X POST -H 'Content-Type: application/json' \
    --data "${REGISTER_BODY}" \
    "${RIMSKY_ENDPOINT}/v1/templates" )"
TEMPLATE_HASH="$( echo "${REGISTER_OUT}" | jq -r '.template_id' )"
if [ -z "${TEMPLATE_HASH}" ] || [ "${TEMPLATE_HASH}" = "null" ]; then
    echo "frame-origin-audit-demo: template register failed: ${REGISTER_OUT}" >&2
    exit 1
fi
echo "frame-origin-audit-demo: template registered as ${TEMPLATE_HASH}"

DEPLOY_OUT="$( curl -sS -X POST -H 'Content-Type: application/json' \
    --data '{}' \
    "${RIMSKY_ENDPOINT}/v1/templates/${TEMPLATE_HASH}/deploy" )"
echo "frame-origin-audit-demo: template deployed: ${DEPLOY_OUT}"

INSTANCE_KEY="frame-origin-audit-demo-$( date +%s )-$$"
INSTANCE_OUT="$( curl -sS -X POST -H 'Content-Type: application/json' \
    --data "{\"template\": \"${TEMPLATE_HASH}\", \"instance_key\": \"${INSTANCE_KEY}\"}" \
    "${RIMSKY_ENDPOINT}/v1/instances" )"
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
    "${RIMSKY_ENDPOINT}/v1/instances/${INSTANCE_ID}/messages" )"
INITIAL_MESSAGE_ID="$( echo "${MESSAGE_OUT}" | jq -r '.message_id' )"
if [ -z "${INITIAL_MESSAGE_ID}" ] || [ "${INITIAL_MESSAGE_ID}" = "null" ]; then
    echo "frame-origin-audit-demo: initial message POST failed: ${MESSAGE_OUT}" >&2
    exit 1
fi
echo "frame-origin-audit-demo: posted kick message ${INITIAL_MESSAGE_ID}"

# Step 5 — poll the cascade-graph endpoint until both the kick frame
# (sender_kind=operator) and the audit/loop frame (sender_kind=instance)
# appear, or the budget runs out.
END=$(( $(date +%s) + POLL_BUDGET_SECONDS ))
SAW_OPERATOR=0
SAW_INSTANCE=0
while [ "$( date +%s )" -lt "${END}" ]; do
    FRAMES_OUT="$( curl -sS \
        "${RIMSKY_ENDPOINT}/v1/instances/${INSTANCE_ID}/frames?limit=100" )"

    # Human-facing demo output: every frame, its triggering message.
    # The format reads "frame=<id> trigger=<msg-id> type=<type>
    # sender=<sender> kind=<sender_kind>" so the operator's eye flows
    # naturally from frame to origin.
    LINES="$( echo "${FRAMES_OUT}" | jq -r '.frames[] |
        "frame=" + .frame_id +
        " trigger=" + .triggering_message_id +
        " type=" + (.message_type // "<missing>") +
        " sender=" + (.message_sender // "<missing>") +
        " kind=" + (.message_sender_kind // "<missing>")' )"
    # Reset the log on every poll so the final assertion runs against
    # the most recent snapshot.
    echo "${LINES}" > "${SELF_CHECK_LOG}"
    echo "${LINES}"

    if echo "${LINES}" | grep -q ' kind=operator'; then
        SAW_OPERATOR=1
    fi
    if echo "${LINES}" | grep -q ' kind=instance'; then
        SAW_INSTANCE=1
    fi
    if [ "${SAW_OPERATOR}" -eq 1 ] && [ "${SAW_INSTANCE}" -eq 1 ]; then
        # Both origins exhibited — continue polling briefly to allow R
        # to settle before we self-check.
        sleep 2
        FRAMES_OUT="$( curl -sS \
            "${RIMSKY_ENDPOINT}/v1/instances/${INSTANCE_ID}/frames?limit=100" )"
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

# Step 6 — self-check the captured output. Spec acceptance:
#   "Every frame carries a pointer back to the message ledger entry
#    that triggered it, surfaced through the existing frames-read
#    observability endpoint."
#
# The structural assertions: every frame line must have (a) a UUID-
# shaped triggering_message_id and (b) a non-empty type + sender. The
# joined-envelope-missing case would surface as `<missing>` in the
# printed format; that's a load-bearing falsifier ("a frame appears
# without an originating message reference").
echo "frame-origin-audit-demo: running self-check on captured observability log"

TOTAL_FRAMES="$( grep -c '^frame=' "${SELF_CHECK_LOG}" || true )"
if [ "${TOTAL_FRAMES}" -eq 0 ]; then
    echo "frame-origin-audit-demo: no frames observed in convergence window" >&2
    exit 1
fi

# Every frame line must carry a UUID-shaped triggering_message_id.
FRAMES_WITH_TRIGGER="$( grep -cE '^frame=[0-9a-fA-F-]{36} trigger=[0-9a-fA-F-]{36} ' "${SELF_CHECK_LOG}" || true )"
if [ "${TOTAL_FRAMES}" -ne "${FRAMES_WITH_TRIGGER}" ]; then
    echo "frame-origin-audit-demo: only ${FRAMES_WITH_TRIGGER}/${TOTAL_FRAMES} frame lines carry a triggering_message_id" >&2
    exit 1
fi

# Every frame line must have a non-empty joined envelope type and
# sender. A `<missing>` in either field means the LEFT JOIN fell back
# — the frames row exists but the join against rimsky_messages failed,
# which violates the FK constraint and the spec acceptance.
if grep -q 'type=<missing>' "${SELF_CHECK_LOG}"; then
    echo "frame-origin-audit-demo: at least one frame has no joined message type" >&2
    exit 1
fi
if grep -q 'sender=<missing>' "${SELF_CHECK_LOG}"; then
    echo "frame-origin-audit-demo: at least one frame has no joined message sender" >&2
    exit 1
fi

# At least one operator-posted origin AND at least one cascade-emit
# origin — exercises the spec's "operator message, a publisher
# message, or a cascade-emitted message" enumeration end-to-end. The
# kick is operator; the emit-node's audit/loop is cascade-emit.
# Without both, the demo only proved one half of the origin surface.
if ! grep -q ' kind=operator' "${SELF_CHECK_LOG}"; then
    echo "frame-origin-audit-demo: no operator-origin frame observed" >&2
    exit 1
fi
if ! grep -q ' kind=instance' "${SELF_CHECK_LOG}"; then
    echo "frame-origin-audit-demo: no cascade-emit (sender_kind=instance) origin frame observed" >&2
    echo "frame-origin-audit-demo: the emit-node back-edge did not fire" >&2
    exit 1
fi

echo "frame-origin-audit-demo: PASS — ${TOTAL_FRAMES} frames observed; every frame carries a triggering_message_id, joined envelope type, and joined sender"
