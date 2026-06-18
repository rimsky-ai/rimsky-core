#!/usr/bin/env bash
# Copyright © 2026 Fall Guy Consulting.
# Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
# repo root, or http://www.apache.org/licenses/LICENSE-2.0.

# @story: client-context — runnable proof. Walks through the full
# `rimsky ctx` lifecycle (add / use / current / rm) against TWO real
# local control-api endpoints so the switch is observable: subsequent
# CLI commands actually hit the endpoint named by the active context,
# not the other one.
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

echo "step: clean"
rm -f "${HOME}/.rimsky/config.yml"

echo "step: add-staging"
rimsky ctx add staging --endpoint "${STAGING_URL}"

echo "step: add-prod"
rimsky ctx add prod --endpoint "${PROD_URL}"

echo "step: list-after-add"
rimsky ctx list

echo "step: use-staging"
rimsky ctx use staging

echo "step: ls-instances-staging"
rimsky ls instances

echo "step: health-endpoint-is-staging"
staging_health=$(rimsky health)
echo "${staging_health}"
echo "${staging_health}" | grep -qE "^endpoint:[[:space:]]+${STAGING_URL}\$" \
  || { echo "demo: expected endpoint: ${STAGING_URL}, got:" >&2; echo "${staging_health}" >&2; exit 1; }

echo "step: use-prod"
rimsky ctx use prod

echo "step: ls-instances-prod"
rimsky ls instances

echo "step: health-endpoint-is-prod"
prod_health=$(rimsky health)
echo "${prod_health}"
echo "${prod_health}" | grep -qE "^endpoint:[[:space:]]+${PROD_URL}\$" \
  || { echo "demo: expected endpoint: ${PROD_URL}, got:" >&2; echo "${prod_health}" >&2; exit 1; }

echo "step: current-is-prod"
current_output=$(rimsky ctx current)
echo "${current_output}"
echo "${current_output}" | grep -q '^prod[[:space:]]' \
  || { echo "demo: expected 'prod' as current context, got: ${current_output}" >&2; exit 1; }
echo "${current_output}" | grep -qF "${PROD_URL}" \
  || { echo "demo: expected current endpoint ${PROD_URL}, got: ${current_output}" >&2; exit 1; }

echo "step: rm-staging"
rimsky ctx rm staging

echo "step: rm-staging-no-longer-resolves"
if rimsky ctx use staging 2>/tmp/ctx-use-staging.err; then
  echo "demo: expected 'ctx use staging' to fail after rm, but it succeeded" >&2
  exit 1
fi
grep -q 'not found' /tmp/ctx-use-staging.err \
  || { echo "demo: expected 'not found' diagnostic, got:"; cat /tmp/ctx-use-staging.err; exit 1; } >&2

echo "step: done"
