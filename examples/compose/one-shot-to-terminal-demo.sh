#!/usr/bin/env bash
# Copyright © 2026 Fall Guy Consulting.
# Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
# license. See LICENSE.agpl and COPYRIGHT at the repo root.
#
# one-shot-to-terminal-demo.sh — STORY-one-shot-to-terminal proof.
#
# Role: operator who wants to drive a compose manifest to terminal in
# one invocation, without standing up rimsky infrastructure first.
#
# What this demo exhibits, by Falsifier:
#  - the verb runs `rimsky compose run` against a two-instance manifest
#    where one instance succeeds and one fails (the literal Proof
#    shape from STORY-one-shot-to-terminal);
#  - the verb returns ON ITS OWN (no kill needed) after every instance
#    reaches terminal;
#  - the exit code is 1 (mixed outcome → any-failure class per the
#    spec's @decision: exit-codes table);
#  - stderr carries a per-instance summary line for each declared
#    instance by name with its outcome
#    (`instance sample-pipeline/ok: success`,
#    `instance sample-pipeline/oops: failure`) — proving per-instance
#    summary is surfaced by name AND by outcome class, not collapsed
#    into a count;
#  - the aggregate summary line names the any-failure reason class
#    (`compose run: any-failure (2 instances)`), proving the
#    classifier observed the per-instance outcomes rather than
#    defaulting.

set -u

repo_root() {
  cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd
}

die() {
  echo "one-shot-to-terminal-demo: $*" >&2
  exit 1
}

step() {
  echo "==> $*"
}

REPO="$(repo_root)"
WORK="$(mktemp -d -t rimsky-one-shot-demo-XXXXXX)"
# @deliberate: stage tempdir cleanup with a trap so a mid-script exit
# still scrubs.
trap 'rm -rf "$WORK"' EXIT

step "stage: $WORK"

RIMSKY_BIN="${RIMSKY_BIN:-}"
if [[ -z "$RIMSKY_BIN" ]]; then
  RIMSKY_BIN="$WORK/rimsky"
  step "build rimsky CLI"
  (cd "$REPO" && go build -o "$RIMSKY_BIN" ./cmd/rimsky) || die "go build ./cmd/rimsky failed"
fi
[[ -x "$RIMSKY_BIN" ]] || die "RIMSKY_BIN ($RIMSKY_BIN) not executable"

STUB_BIN="$WORK/stub-executor"
step "build stub executor"
(cd "$REPO" && go build -o "$STUB_BIN" ./cmd/rimsky/cli/compose/testdata/stub-executor) \
  || die "go build stub-executor failed"

# @constraint: copy the sample manifest into the working dir so
# .rimsky/ lands in the tempdir; the verb's artifact-root discovery
# walks up from cwd.
cp -R "$REPO/cmd/rimsky/cli/compose/testdata/sample-manifest"/* "$WORK"/
cd "$WORK" || die "cd $WORK"

LOG="$WORK/run.stderr"
step "run rimsky compose run"
set +e
"$RIMSKY_BIN" compose run --service "stub=$STUB_BIN" ./rimsky-compose.yml 2>"$LOG"
rc=$?
set -e

step "exit code: $rc"
echo "----- stderr (tail) -----"
tail -n 20 "$LOG"
echo "-------------------------"

# @constraint: falsifier #1 — rc must be 1; one instance terminal-
# success and one terminal-failure classify as ReasonAnyFailure per
# @decision: exit-codes, so the verb returns exit 1.
[[ "$rc" == "1" ]] || die "FAIL: expected exit code 1 (any-failure for mixed outcome); got $rc"

# @constraint: falsifier #2 — per-instance summary lines must appear
# by name AND carry the outcome class. A count-only output is the
# failure mode the story rules out.
project="sample-pipeline"
grep -qE "instance ${project}/ok: success" "$LOG" \
  || die "FAIL: missing 'instance ${project}/ok: success' summary line"
grep -qE "instance ${project}/oops: failure" "$LOG" \
  || die "FAIL: missing 'instance ${project}/oops: failure' summary line"

# @constraint: falsifier #3 — aggregate summary must name the any-
# failure reason, proving the exit-class classification was driven by
# observed outcomes.
grep -qE "compose run: any-failure \(2 instances\)" "$LOG" \
  || die "FAIL: missing 'compose run: any-failure (2 instances)' aggregate summary"

step "PASS"
echo "STORY-one-shot-to-terminal proven (mixed-outcome leg):"
echo "  - rimsky compose run drove a two-instance manifest to terminal"
echo "  - one instance succeeded, one failed; both reported by name"
echo "  - verb exited 1 (any-failure) on its own"
exit 0
