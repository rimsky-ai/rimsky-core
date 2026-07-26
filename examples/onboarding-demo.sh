#!/usr/bin/env bash
# Copyright © 2026 Fall Guy Consulting.
# Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
# repo root, or http://www.apache.org/licenses/LICENSE-2.0.

# @story: operator-onboarding
set -euo pipefail

SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
TEMPLATE_PATH="${SCRIPT_DIR}/onboarding-template.yaml"

RIMSKY_CONTROL_API_URL="${RIMSKY_CONTROL_API_URL:-http://127.0.0.1:8080}"

RIMSKY_BIN="${RIMSKY_BIN:-rimsky}"

echo "onboarding-demo: registering + deploying + instantiating ${TEMPLATE_PATH} against ${RIMSKY_CONTROL_API_URL}"

RUN_STDOUT="$( "${RIMSKY_BIN}" run \
    --endpoint "${RIMSKY_CONTROL_API_URL}" \
    --instance-key "onboarding-demo-$( date +%s )-$$" \
    "${TEMPLATE_PATH}" )"
echo "${RUN_STDOUT}"

INSTANCE_ID="$( printf '%s\n' "${RUN_STDOUT}" \
    | sed -n 's/^instance_id=\([0-9a-fA-F-]\{36\}\)[[:space:]]*$/\1/p' \
    | head -n1 )"
if [ -z "${INSTANCE_ID}" ]; then
    echo "onboarding-demo: 'rimsky run' did not print 'instance_id=<uuid>'" >&2
    echo "onboarding-demo: stdout was:" >&2
    echo "${RUN_STDOUT}" >&2
    exit 1
fi

echo "onboarding-demo: instance_id=${INSTANCE_ID} — watching to terminal"

"${RIMSKY_BIN}" watch \
    --endpoint "${RIMSKY_CONTROL_API_URL}" \
    --poll-interval 250ms \
    "${INSTANCE_ID}"

echo "onboarding-demo: instance ${INSTANCE_ID} reached terminal — dev-loop walkthrough complete"
