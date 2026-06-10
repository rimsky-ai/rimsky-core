#!/usr/bin/env bash
# Copyright © 2026 Fall Guy Consulting.
# Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
# repo root, or http://www.apache.org/licenses/LICENSE-2.0.

#
# client-context-demo.sh — runnable proof of STORY-client-context.
#
# Walks through the full `rimsky ctx` lifecycle (add / use / current / rm)
# against TWO real local control-api endpoints so the switch is
# observable: subsequent CLI commands actually hit the endpoint named by
# the active context, not the other one.
#
# Assumed-already-running state (the script does NOT bring up the
# stacks; the driver test cmd/rimsky/cli/ctx_demo_test.go does that):
#
#   * Two `rimsky-all-in-one` containers, each on its own host port.
#   * Their host-mapped base URLs are passed in via STAGING_URL and
#     PROD_URL env vars.
#   * The rimsky CLI binary is on PATH (the driver test builds it from
#     ./cmd/rimsky/ and prepends a temp bin/ dir to PATH).
#   * HOME points at an empty tempdir so this run's config writes do
#     not stomp the developer's real ~/.rimsky/config.yml.
#
# Each step prints a `step: <name>` marker line on stdout so the driver
# test can assert the script reached the right points in the right
# order. Failures `set -e`'s the script with a non-zero exit.
#
# Run by hand (after `make core-images` and after starting two
# rimsky-all-in-one containers manually):
#
#   STAGING_URL=http://127.0.0.1:18080 \
#   PROD_URL=http://127.0.0.1:18081 \
#   HOME=$(mktemp -d) \
#     bash examples/client-context-demo.sh

set -euo pipefail

: "${STAGING_URL:?STAGING_URL is required (base URL of the staging rimsky-all-in-one stack)}"
: "${PROD_URL:?PROD_URL is required (base URL of the prod rimsky-all-in-one stack)}"

if ! command -v rimsky >/dev/null 2>&1; then
  echo "demo: rimsky CLI not on PATH" >&2
  exit 1
fi

# step: clean — start from an empty ~/.rimsky/config.yml.
echo "step: clean"
rm -f "${HOME}/.rimsky/config.yml"

# step: add-staging — register the first context. After this, the staging
# context exists and (because it's the first) is implicitly current.
echo "step: add-staging"
rimsky ctx add staging --endpoint "${STAGING_URL}"

# step: add-prod — register the second context. The current context
# stays on staging (the first one); we'll switch in the next step.
echo "step: add-prod"
rimsky ctx add prod --endpoint "${PROD_URL}"

# step: list-after-add — assert both contexts exist with their endpoints.
echo "step: list-after-add"
rimsky ctx list

# step: use-staging — switch the active context to staging. From this
# point on, every CLI command without an explicit --endpoint hits
# STAGING_URL.
echo "step: use-staging"
rimsky ctx use staging

# step: ls-instances-staging — observable proof that the active-context
# endpoint resolves to STAGING_URL: `rimsky ls instances` hits the
# staging control-api. A successful exit here means the CLI reached
# the staging stack; if the switch were silently a no-op against an
# unreachable URL the command would error out.
echo "step: ls-instances-staging"
rimsky ls instances

# step: health-endpoint-is-staging — `rimsky health` prints the
# resolved control-api URL on its own line (the `endpoint` key in the
# human KV output and the `endpoint:` field in JSON). After `use
# staging` the printed endpoint MUST be STAGING_URL. This is the
# observable that prevents a silent no-op `ctx use` from passing the
# falsifier: if the switch were a no-op and the CLI kept hitting some
# other endpoint, the printed line would not match STAGING_URL.
echo "step: health-endpoint-is-staging"
staging_health=$(rimsky health)
echo "${staging_health}"
echo "${staging_health}" | grep -qE "^endpoint:[[:space:]]+${STAGING_URL}\$" \
  || { echo "demo: expected endpoint: ${STAGING_URL}, got:" >&2; echo "${staging_health}" >&2; exit 1; }

# step: use-prod — switch the active context. Subsequent commands now
# hit PROD_URL.
echo "step: use-prod"
rimsky ctx use prod

# step: ls-instances-prod — observable proof that the switch is honored
# by the next command: `rimsky ls instances` now hits the prod
# control-api, not the staging one we hit a moment ago.
echo "step: ls-instances-prod"
rimsky ls instances

# step: health-endpoint-is-prod — same observable as above, against
# the prod URL. Combined with the staging assertion above, this is
# the proof that the active-context switch is picked up by the next
# command (the spec's first falsifier clause). The two URLs differ;
# both assertions cannot pass unless the CLI is actually consulting
# the active context for each subsequent command.
echo "step: health-endpoint-is-prod"
prod_health=$(rimsky health)
echo "${prod_health}"
echo "${prod_health}" | grep -qE "^endpoint:[[:space:]]+${PROD_URL}\$" \
  || { echo "demo: expected endpoint: ${PROD_URL}, got:" >&2; echo "${prod_health}" >&2; exit 1; }

# step: current-is-prod — `rimsky ctx current` names the active context
# and its endpoint. After `use prod` it must name prod / PROD_URL.
echo "step: current-is-prod"
current_output=$(rimsky ctx current)
echo "${current_output}"
echo "${current_output}" | grep -q '^prod[[:space:]]' \
  || { echo "demo: expected 'prod' as current context, got: ${current_output}" >&2; exit 1; }
echo "${current_output}" | grep -qF "${PROD_URL}" \
  || { echo "demo: expected current endpoint ${PROD_URL}, got: ${current_output}" >&2; exit 1; }

# step: rm-staging — remove a non-current context. After this, attempts
# to `ctx use staging` fail (no such context).
echo "step: rm-staging"
rimsky ctx rm staging

# step: rm-staging-no-longer-resolves — the falsifier ("removed context
# still resolves") is exhibited as a failure here: if `rm` left
# `staging` resolvable, the next `ctx use staging` would succeed. It
# must fail with the "not found" diagnostic.
echo "step: rm-staging-no-longer-resolves"
if rimsky ctx use staging 2>/tmp/ctx-use-staging.err; then
  echo "demo: expected 'ctx use staging' to fail after rm, but it succeeded" >&2
  exit 1
fi
grep -q 'not found' /tmp/ctx-use-staging.err \
  || { echo "demo: expected 'not found' diagnostic, got:"; cat /tmp/ctx-use-staging.err; exit 1; } >&2

# step: done — all steps green.
echo "step: done"
