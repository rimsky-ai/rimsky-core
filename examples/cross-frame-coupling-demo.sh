#!/usr/bin/env bash
# Copyright © 2026 Fall Guy Consulting.
# Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
# repo root, or http://www.apache.org/licenses/LICENSE-2.0.

# cross-frame-coupling-demo.sh — STORY-cross-frame-coupling proof.
#
# A template author expresses cross-frame coupling through the typed-
# message machinery: node A runs, emit-node E composes a loop/iterate
# message from A's data, node R wakes in the next frame and reads
# {{messages.loop/iterate.<field>}} as its inputs. The script:
#
#   1. Registers + deploys the demo template.
#   2. Creates an instance.
#   3. Posts the initial wake-up message to /v1/instances/{id}/messages.
#   4. Polls the cascade-graph endpoint (/v1/instances/{id}/frames)
#      until both the wake frame and the iterate frame appear.
#   5. For each frame, prints triggering_message_id + the joined
#      envelope's type — so a reader sees the back-edge flow.
#   6. Self-checks: greps the captured output for required strings.
#      If the wake frame is missing or the loop/iterate back-edge
#      frame is missing or any frame line lacks a triggering_message_id,
#      the script exits non-zero.
#
# Prerequisites the operator must satisfy BEFORE running this script:
#
#   1. A running rimsky stack reachable at RIMSKY_ENDPOINT
#      (default http://127.0.0.1:8080).
#
#   2. The bundled `verifier-shape-checks` executor reachable from the
#      stack — declared in rimsky.yml under `executors:`. The
#      services-scenarios driver test
#      (lib/services/test/scenarios/cross_frame_coupling_demo_e2e_test.go)
#      wires this automatically via testcontainers. For a bare-metal
#      stack the operator must declare the executor (and run the
#      verifier-shape-checks container or binary). See
#      examples/README.md for the wiring.
#
#   3. `curl`, `jq`, and `yq` on $PATH for the HTTP/JSON/YAML handling.
#
# Output discipline: exits 0 only when EVERY frame line in the captured
# observability output carries a non-empty triggering_message_id AND
# the captured output contains both the wake frame (type=loop/wake) and
# the back-edge frame (type=loop/iterate). Anything else exits non-zero
# with a diagnostic.

set -euo pipefail

# Resolve the script's own directory so the template path works whether
# the operator runs the script from the repo root or via an absolute
# path.
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
TEMPLATE_PATH="${SCRIPT_DIR}/cross-frame-coupling-demo-template.yaml"

# Endpoint defaults to the all-in-one image's mapped local port; the
# test harness overrides it to point at a testcontainers-mapped port.
RIMSKY_ENDPOINT="${RIMSKY_ENDPOINT:-http://127.0.0.1:8080}"

# The convergence-poll budget. The cycle runs through several
# scheduler ticks; 60s is generous on a local stack and short enough
# that a runaway loop or a stuck cascade exits as a real failure.
POLL_BUDGET_SECONDS="${POLL_BUDGET_SECONDS:-60}"

# Self-checking output: every line printed by the polling step gets
# captured. The grep assertions run against this file at the end so
# the falsifier is structural — a missing back-edge frame trips the
# grep, not just a flaky timing-based assertion.
SELF_CHECK_LOG="$( mktemp -t cross-frame-coupling-demo.XXXXXXXX )"
cleanup() {
    local rc=$?
    # Surface the captured log if we're exiting non-zero so a CI run's
    # operator can read the cascade output without re-running.
    if [ "${rc}" -ne 0 ]; then
        echo "" >&2
        echo "cross-frame-coupling-demo: captured observability log follows:" >&2
        cat "${SELF_CHECK_LOG}" >&2
    fi
    rm -f "${SELF_CHECK_LOG}"
    exit "${rc}"
}
trap cleanup EXIT

# Translate the YAML template to JSON. The control-api accepts both
# YAML and JSON bodies on POST /v1/templates, but the wrapping
# {"spec": ...} shape is mandatory. The script does the YAML→JSON
# conversion inline so it has no Python dependency beyond what a
# typical dev box already carries; if `yq` isn't available the
# operator can pre-convert the template by hand.
which yq >/dev/null 2>&1 || { echo "cross-frame-coupling-demo: yq not on PATH" >&2; exit 2; }
which curl >/dev/null 2>&1 || { echo "cross-frame-coupling-demo: curl not on PATH" >&2; exit 2; }
which jq >/dev/null 2>&1 || { echo "cross-frame-coupling-demo: jq not on PATH" >&2; exit 2; }

echo "cross-frame-coupling-demo: registering template at ${RIMSKY_ENDPOINT}"

# Step 1 — register + deploy the template.
SPEC_JSON="$( yq -o=json '.' "${TEMPLATE_PATH}" )"
REGISTER_BODY="$( jq -n --argjson spec "${SPEC_JSON}" '{spec: $spec}' )"
REGISTER_OUT="$( curl -sS -X POST -H 'Content-Type: application/json' \
    --data "${REGISTER_BODY}" \
    "${RIMSKY_ENDPOINT}/v1/templates" )"
TEMPLATE_HASH="$( echo "${REGISTER_OUT}" | jq -r '.template_id' )"
if [ -z "${TEMPLATE_HASH}" ] || [ "${TEMPLATE_HASH}" = "null" ]; then
    echo "cross-frame-coupling-demo: template register failed: ${REGISTER_OUT}" >&2
    exit 1
fi
echo "cross-frame-coupling-demo: template registered as ${TEMPLATE_HASH}"

# Step 2 — deploy the template (transition register → deployed).
DEPLOY_OUT="$( curl -sS -X POST -H 'Content-Type: application/json' \
    --data '{}' \
    "${RIMSKY_ENDPOINT}/v1/templates/${TEMPLATE_HASH}/deploy" )"
echo "cross-frame-coupling-demo: template deployed: ${DEPLOY_OUT}"

# Step 3 — create an instance.
INSTANCE_KEY="cross-frame-coupling-demo-$( date +%s )-$$"
INSTANCE_OUT="$( curl -sS -X POST -H 'Content-Type: application/json' \
    --data "{\"template\": \"${TEMPLATE_HASH}\", \"instance_key\": \"${INSTANCE_KEY}\"}" \
    "${RIMSKY_ENDPOINT}/v1/instances" )"
INSTANCE_ID="$( echo "${INSTANCE_OUT}" | jq -r '.instance_id' )"
if [ -z "${INSTANCE_ID}" ] || [ "${INSTANCE_ID}" = "null" ]; then
    echo "cross-frame-coupling-demo: instance create failed: ${INSTANCE_OUT}" >&2
    exit 1
fi
echo "cross-frame-coupling-demo: instance ${INSTANCE_ID} created"

# Step 4 — POST the initial wake message. Every emit must carry an
# Idempotency-Key (concept:message). Using a uuid-shaped value so the
# replay-dedup contract is exercised end-to-end.
IDEMPOTENCY_KEY="cfcd-wake-$( uuidgen 2>/dev/null || echo "${INSTANCE_KEY}" )"
MESSAGE_BODY='{"type":"loop/wake","payload":{"trip_counter":0}}'
MESSAGE_OUT="$( curl -sS -X POST -H 'Content-Type: application/json' \
    -H "Idempotency-Key: ${IDEMPOTENCY_KEY}" \
    --data "${MESSAGE_BODY}" \
    "${RIMSKY_ENDPOINT}/v1/instances/${INSTANCE_ID}/messages" )"
INITIAL_MESSAGE_ID="$( echo "${MESSAGE_OUT}" | jq -r '.message_id' )"
if [ -z "${INITIAL_MESSAGE_ID}" ] || [ "${INITIAL_MESSAGE_ID}" = "null" ]; then
    echo "cross-frame-coupling-demo: initial message POST failed: ${MESSAGE_OUT}" >&2
    exit 1
fi
echo "cross-frame-coupling-demo: posted initial wake message ${INITIAL_MESSAGE_ID}"

# Step 5 — poll the cascade-graph endpoint until BOTH the wake frame and
# the iterate frame appear, OR the budget runs out. The polling output is
# captured both to stdout (for the human-facing demo) and to the self-
# check log (for the structural grep).
END=$(( $(date +%s) + POLL_BUDGET_SECONDS ))
SAW_WAKE=0
SAW_ITERATE=0
while [ "$( date +%s )" -lt "${END}" ]; do
    FRAMES_OUT="$( curl -sS \
        "${RIMSKY_ENDPOINT}/v1/instances/${INSTANCE_ID}/frames?limit=100" )"

    # Print each frame line in the human-facing demo format.
    # Format: "frame=<id> state=<state> trigger=<msg-id> type=<msg-type>"
    LINES="$( echo "${FRAMES_OUT}" | jq -r '.frames[] | "frame=" + .frame_id + " state=" + .state + " trigger=" + .triggering_message_id + " type=" + (.message_type // "<missing>")' )"
    # Reset the log on every poll so the final assertion runs against
    # the most recent snapshot (frames can change state from running to
    # ended between polls; we want the final view).
    echo "${LINES}" > "${SELF_CHECK_LOG}"
    echo "${LINES}"

    if echo "${LINES}" | grep -q 'type=loop/wake'; then
        SAW_WAKE=1
    fi
    if echo "${LINES}" | grep -q 'type=loop/iterate'; then
        SAW_ITERATE=1
    fi
    if [ "${SAW_WAKE}" -eq 1 ] && [ "${SAW_ITERATE}" -eq 1 ]; then
        # Both frames exhibited — continue polling briefly to allow R
        # to settle into its terminal state before we self-check.
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

# Step 6 — self-check the captured output. The structural assertions
# are the falsifier the spec calls out: every frame must have a
# non-empty triggering_message_id; at least one frame must be the
# back-edge frame (joined message type = loop/iterate).
echo "cross-frame-coupling-demo: running self-check on captured observability log"

# Count frame lines and lines that DO have a non-empty
# triggering_message_id. A frame line whose trigger field is empty
# (`trigger= ` with no UUID) violates the spec acceptance "every frame
# carries a pointer back to the message ledger entry that triggered it."
TOTAL_FRAMES="$( grep -c '^frame=' "${SELF_CHECK_LOG}" || true )"
FRAMES_WITH_TRIGGER="$( grep -cE '^frame=[^ ]+ state=[^ ]+ trigger=[0-9a-fA-F-]{36} ' "${SELF_CHECK_LOG}" || true )"
if [ "${TOTAL_FRAMES}" -eq 0 ]; then
    echo "cross-frame-coupling-demo: no frames observed in convergence window" >&2
    exit 1
fi
if [ "${TOTAL_FRAMES}" -ne "${FRAMES_WITH_TRIGGER}" ]; then
    echo "cross-frame-coupling-demo: only ${FRAMES_WITH_TRIGGER}/${TOTAL_FRAMES} frame lines carry a triggering_message_id" >&2
    exit 1
fi

# At least one frame must be the back-edge frame (the emit-node's
# loop/iterate message opened it). Without this, the cascade exhibited
# only the wake → A chain and the emit-node never fired — the
# "the next frame opens carrying that message" leg failed.
if ! grep -q 'type=loop/iterate' "${SELF_CHECK_LOG}"; then
    echo "cross-frame-coupling-demo: no frame opened by the loop/iterate back-edge message" >&2
    echo "cross-frame-coupling-demo: the back-edge cascade did not fire (E never emitted)" >&2
    exit 1
fi

# At least one frame must be the initial wake frame (type=loop/wake).
# Without this, the demo's initial POST never reached the cascade
# walker.
if ! grep -q 'type=loop/wake' "${SELF_CHECK_LOG}"; then
    echo "cross-frame-coupling-demo: no frame opened by the loop/wake initial message" >&2
    echo "cross-frame-coupling-demo: the wake → cascade leg failed" >&2
    exit 1
fi

echo "cross-frame-coupling-demo: PASS — ${TOTAL_FRAMES} frames observed; every frame carries a triggering_message_id; both wake and back-edge messages opened frames"
