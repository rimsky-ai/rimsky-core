#!/usr/bin/env bash
# Copyright © 2026 Fall Guy Consulting.
# Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
# repo root, or http://www.apache.org/licenses/LICENSE-2.0.

# @story: operator-onboarding — the README's first-steps walkthrough.
#
# A new operator with no prior rimsky experience copies the shipped
# example TemplateSpec (`examples/onboarding-template.yaml`), runs a
# single CLI verb against their local stack, and watches the resulting
# instance run to a terminal state.
#
# Prerequisites the operator must satisfy BEFORE running this script:
#
#   1. A running rimsky stack reachable at RIMSKY_ENDPOINT (default
#      http://127.0.0.1:8080). For local dev, the easiest path is
#      `docker run --rm -p 8080:8080 rimsky-all-in-one:latest`.
#   2. The bundled verifier-shape-checks executor reachable from the
#      stack. The driver test under
#      `lib/services/test/scenarios/onboarding_demo_e2e_test.go` wires
#      this automatically via testcontainers; for a bare-metal stack the
#      operator declares it in rimsky.yml — see
#      `examples/README.md` for the wiring.
#   3. The `rimsky` CLI binary on $PATH. For an in-repo run, the test
#      drives `cli.RunRun` and `cli.RunWatch` directly in-process; for a
#      bare-metal run, `make cli` builds it.
#
# Output discipline: exits 0 only when `rimsky run` printed a real
# instance_id and `rimsky watch` exited cleanly after the instance
# reached a terminal state. Anything else (missing instance_id, non-zero
# from `rimsky run`, watch timing out) exits non-zero.

set -euo pipefail

SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
TEMPLATE_PATH="${SCRIPT_DIR}/onboarding-template.yaml"

RIMSKY_ENDPOINT="${RIMSKY_ENDPOINT:-http://127.0.0.1:8080}"

RIMSKY_BIN="${RIMSKY_BIN:-rimsky}"

echo "onboarding-demo: registering + deploying + instantiating ${TEMPLATE_PATH} against ${RIMSKY_ENDPOINT}"

RUN_STDOUT="$( "${RIMSKY_BIN}" run \
    --endpoint "${RIMSKY_ENDPOINT}" \
    --instance-key "onboarding-demo-$( date +%s )-$$" \
    --terminate-after-run \
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
    --endpoint "${RIMSKY_ENDPOINT}" \
    --poll-interval 250ms \
    "${INSTANCE_ID}"

echo "onboarding-demo: instance ${INSTANCE_ID} reached terminal — dev-loop walkthrough complete"
