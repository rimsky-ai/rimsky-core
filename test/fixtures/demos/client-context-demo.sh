#!/usr/bin/env bash
# Copyright © 2026 Fall Guy Consulting.
# SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

# @story: client-context

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
