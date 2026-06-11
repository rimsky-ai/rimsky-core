#!/usr/bin/env bash
# Copyright © 2026 Fall Guy Consulting.
# Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
# repo root, or http://www.apache.org/licenses/LICENSE-2.0.

# onboarding-demo.sh — the README's first-steps walkthrough.
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

# Resolve the script's own directory so the template path works whether
# the operator runs the script from the repo root or via an absolute
# path. The shipped template sits next to this script.
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
TEMPLATE_PATH="${SCRIPT_DIR}/onboarding-template.yaml"

# The endpoint defaults to the all-in-one image's mapped local port
# (`docker run -p 8080:8080 rimsky-all-in-one:latest`); the test
# harness overrides it to point at the testcontainers-mapped port.
RIMSKY_ENDPOINT="${RIMSKY_ENDPOINT:-http://127.0.0.1:8080}"

# Allow the test harness to inject an explicit `rimsky` binary path
# (the in-process test compiles a temp binary). When unset, the
# script falls back to the binary on $PATH — the bare-metal path.
RIMSKY_BIN="${RIMSKY_BIN:-rimsky}"

echo "onboarding-demo: registering + deploying + instantiating ${TEMPLATE_PATH} against ${RIMSKY_ENDPOINT}"

# Step 1 — run the headline dev-loop verb. `rimsky run <file>` performs
# register + deploy + instantiate in one shot. With --keep (the
# default) it prints `instance_id=<uuid>` to stdout and exits 0; the
# `instance_key` keeps repeated demo runs disjoint.
#
# `--terminate-after-run` opts the instance into self-termination once
# its nodes settle. Without it, durable-by-default keeps the instance
# alive past node-terminal — the subsequent `rimsky watch` polls for
# `terminated_at` and would never see it flip.
RUN_STDOUT="$( "${RIMSKY_BIN}" run \
    --endpoint "${RIMSKY_ENDPOINT}" \
    --instance-key "onboarding-demo-$( date +%s )-$$" \
    --terminate-after-run \
    "${TEMPLATE_PATH}" )"
echo "${RUN_STDOUT}"

# Extract the instance ID from the `instance_id=<uuid>` line. Anything
# else (no line, non-UUID) is a real defect — the README's documented
# dev loop must print the line every time.
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

# Step 2 — tail the instance's event stream through the real CLI verb.
# RunWatch polls the unified event log + the instance terminal flag and
# exits 0 once the instance terminates. The 250ms poll keeps the demo
# snappy; the timeout-on-the-test-side is the failure surface if the
# instance never settles.
"${RIMSKY_BIN}" watch \
    --endpoint "${RIMSKY_ENDPOINT}" \
    --poll-interval 250ms \
    "${INSTANCE_ID}"

echo "onboarding-demo: instance ${INSTANCE_ID} reached terminal — dev-loop walkthrough complete"
