#!/usr/bin/env bash
# Copyright © 2026 Fall Guy Consulting.
# Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
# license. See LICENSE.agpl and COPYRIGHT at the repo root.
set -uo pipefail

out=$(mktemp)
trap 'rm -f "$out"' EXIT

go test "$@" 2>&1 | tee "$out"
status=$?

if grep -q "panic: test timed out" "$out"; then
  echo ""
  echo "✖✖ TEST BINARY TIMED OUT ✖✖"
  echo "A test package hit the -timeout ceiling and was KILLED mid-run."
  echo "Tests that never reported are NOT passes — this run's results are incomplete."
  echo "Raise the -timeout for this target or fix the slow/hanging tests before trusting this suite."
  exit 1
fi

exit "$status"
