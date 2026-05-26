#!/usr/bin/env bash
# Copyright © 2026 Fall Guy Consulting.
# Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
# repo root, or http://www.apache.org/licenses/LICENSE-2.0.

# release.sh — rimsky release driver.
#
# Stub release script introduced by P4 of
# spec:2026-05-24-repo-reorganization-design. Today the only first-class
# responsibility implemented here is the pre-release docs-reconciliation
# gate: invoke the docs-lint binaries from a sibling rimsky-docs checkout
# with RIMSKY_REPO pointing at this rimsky tree. Drift blocks the release.
#
# Future work: tagging (root + sdk/go lockstep), pushing tags, producing
# release notes. Today the script stops after the gate; the operator
# performs the remaining steps manually.
#
# Usage:
#   scripts/release.sh                          # run the gate, then stop
#   scripts/release.sh --skip-docs-reconciliation   # bypass the gate
#
# Environment:
#   RIMSKY_DOCS_REPO   path to rimsky-docs checkout (default: ../rimsky-docs)
#
# Exit codes:
#   0  gate passed (or skipped); ready for the operator to tag
#   1  gate failed — docs and rimsky source have drifted; reconcile in a
#      rimsky-docs PR before retrying
#   2  bad invocation (unknown flag, missing rimsky-docs checkout, etc.)

set -euo pipefail

SKIP_DOCS_RECONCILIATION=0
for arg in "$@"; do
  case "$arg" in
    --skip-docs-reconciliation)
      SKIP_DOCS_RECONCILIATION=1
      ;;
    -h|--help)
      sed -n '1,/^$/p' "$0" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *)
      echo "release.sh: unknown argument: $arg" >&2
      echo "see scripts/release.sh --help" >&2
      exit 2
      ;;
  esac
done

RIMSKY_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
RIMSKY_DOCS_REPO="${RIMSKY_DOCS_REPO:-${RIMSKY_ROOT}/../rimsky-docs}"

if [ "$SKIP_DOCS_RECONCILIATION" = "1" ]; then
  echo "release.sh: --skip-docs-reconciliation set; bypassing docs-lint gate." >&2
  echo "release.sh: gate bypassed; operator may proceed to tag." >&2
  exit 0
fi

if [ ! -d "$RIMSKY_DOCS_REPO" ]; then
  echo "release.sh: rimsky-docs checkout not found at $RIMSKY_DOCS_REPO." >&2
  echo "release.sh: clone https://github.com/fallguyconsulting/rimsky-docs next to this rimsky checkout, or set RIMSKY_DOCS_REPO=<path>." >&2
  echo "release.sh: use --skip-docs-reconciliation for emergency releases." >&2
  exit 2
fi

if [ ! -d "$RIMSKY_DOCS_REPO/cmd" ]; then
  echo "release.sh: $RIMSKY_DOCS_REPO/cmd not found; rimsky-docs checkout looks incomplete." >&2
  exit 2
fi

echo "release.sh: running docs-lint gate (RIMSKY_REPO=$RIMSKY_ROOT) ..." >&2
(
  cd "$RIMSKY_DOCS_REPO/cmd"
  RIMSKY_REPO="$RIMSKY_ROOT" go run ./rimsky-docs-lint all
)
echo "release.sh: docs-lint gate passed; operator may proceed to tag." >&2
