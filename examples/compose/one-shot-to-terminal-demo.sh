#!/usr/bin/env bash
# Copyright © 2026 Fall Guy Consulting.
# Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
# repo root, or http://www.apache.org/licenses/LICENSE-2.0.

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

[[ "$rc" == "1" ]] || die "FAIL: expected exit code 1 (any-failure for mixed outcome); got $rc"

project="sample-pipeline"
grep -qE "instance ${project}/ok: success" "$LOG" \
  || die "FAIL: missing 'instance ${project}/ok: success' summary line"
grep -qE "instance ${project}/oops: failure" "$LOG" \
  || die "FAIL: missing 'instance ${project}/oops: failure' summary line"

grep -qE "compose run: any-failure \(2 instances\)" "$LOG" \
  || die "FAIL: missing 'compose run: any-failure (2 instances)' aggregate summary"

step "PASS"
echo "STORY-one-shot-to-terminal proven (mixed-outcome leg):"
echo "  - rimsky compose run drove a two-instance manifest to terminal"
echo "  - one instance succeeded, one failed; both reported by name"
echo "  - verb exited 1 (any-failure) on its own"
exit 0
