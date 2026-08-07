#!/usr/bin/env bash
# Copyright © 2026 Fall Guy Consulting.
# SPDX-License-Identifier: Apache-2.0

set -u

script_dir() {
  cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd
}

die() {
  echo "live-progress-demo: $*" >&2
  exit 1
}

step() {
  echo "==> $*"
}

HERE="$(script_dir)"
WORK="$(mktemp -d -t rimsky-live-demo-XXXXXX)"
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

TRANSCRIPT="$WORK/run.transcript"
step "run rimsky compose run; pipe stderr through a per-line timestamper"

set +e
"$RIMSKY_BIN" compose run \
  --service "stub=$STUB_BIN" \
  --timeout 60s \
  ./rimsky-compose-live.yml 2> >(while IFS= read -r line; do
    printf '%s %s\n' "$(date +%s.%N)" "$line"
  done >"$TRANSCRIPT") >/dev/null
rc=$?
set -e

step "verb exited with code: $rc"
[[ "$rc" == "0" ]] || die "FAIL: verb exited $rc; expected 0 (live-progress demo uses an all-success manifest)"

echo "----- transcript (per-instance summary lines only) -----"
grep -E "instance live-pipeline/(fast|slow): success" "$TRANSCRIPT" || true
echo "--------------------------------------------------------"

fast_line=$(grep -E "instance live-pipeline/fast: success" "$TRANSCRIPT" | head -n 1 || true)
slow_line=$(grep -E "instance live-pipeline/slow: success" "$TRANSCRIPT" | head -n 1 || true)
[[ -n "$fast_line" ]] || die "FAIL: 'live-pipeline/fast: success' missing from transcript"
[[ -n "$slow_line" ]] || die "FAIL: 'live-pipeline/slow: success' missing from transcript"

fast_ts=$(printf '%s' "$fast_line" | awk '{print $1}')
slow_ts=$(printf '%s' "$slow_line" | awk '{print $1}')
[[ -n "$fast_ts" && -n "$slow_ts" ]] || die "FAIL: transcript lines missing timestamp prefix"

delta=$(awk -v a="$slow_ts" -v b="$fast_ts" 'BEGIN { printf "%.3f", a - b }')
step "fast terminal at $fast_ts; slow terminal at $slow_ts; delta=${delta}s"

ok=$(awk -v d="$delta" 'BEGIN { print (d >= 1.0) ? "yes" : "no" }')
[[ "$ok" == "yes" ]] \
  || die "FAIL: fast terminal arrived within ${delta}s of slow terminal (expected ≥1.0s gap) — verb appears to be buffering progress lines"

sane=$(awk -v d="$delta" 'BEGIN { print (d <= 6.0) ? "yes" : "no" }')
[[ "$sane" == "yes" ]] \
  || die "FAIL: delta=${delta}s is greater than the expected 6s upper bound; instances may be serializing rather than running concurrently"

step "PASS"
echo "STORY-live-progress proven:"
echo "  - the fast instance's terminal line arrived ${delta}s before the slow instance's"
echo "  - the transcript timestamps prove progress is emitted live, not batched"
exit 0
