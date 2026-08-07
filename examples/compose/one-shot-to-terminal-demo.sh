#!/usr/bin/env bash
# Copyright © 2026 Fall Guy Consulting.
# SPDX-License-Identifier: Apache-2.0

set -u

script_dir() {
  cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd
}

die() {
  echo "one-shot-to-terminal-demo: $*" >&2
  exit 1
}

step() {
  echo "==> $*"
}

HERE="$(script_dir)"
WORK="$(mktemp -d -t rimsky-one-shot-demo-XXXXXX)"
trap 'rm -rf "$WORK"' EXIT

step "stage: $WORK"

RIMSKY_BIN="${RIMSKY_BIN:-rimsky}"
command -v "$RIMSKY_BIN" >/dev/null 2>&1 \
  || die "RIMSKY_BIN ($RIMSKY_BIN) not found on PATH; set RIMSKY_BIN to a rimsky binary (e.g. \`go build -o /path/to/rimsky ./cmd/rimsky\` from a rimsky-core checkout)"

STUB_BIN="$WORK/stub-executor"
step "build stub executor"
(cd "$HERE/stub-executor" && go build -o "$STUB_BIN" .) \
  || die "go build stub-executor failed"

cp -R "$HERE/sample-manifest"/* "$WORK"/
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
