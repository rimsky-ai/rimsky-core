#!/usr/bin/env bash
# Copyright © 2026 Fall Guy Consulting.
# Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
# license. See LICENSE.agpl and COPYRIGHT at the repo root.
#
# live-progress-demo.sh — STORY-live-progress proof.
#
# Role: operator watching a one-shot run unfold — wants to see per-
# instance and per-node terminal lines emitted IN TIME with execution,
# not buffered until the run ends.
#
# What this demo exhibits, by Falsifier:
#  - the verb's stderr is captured line-by-line into a timestamped
#    transcript while the run is in progress (each transcript line
#    is prefixed with the wall-clock time at which the line crossed
#    the pipe);
#  - the manifest has two instances: `fast` (no delay) and `slow`
#    (stub executor's delay_ms attribute holds dispatch for 3s);
#  - the transcript shows the `fast: success` line at a wall-clock
#    time at LEAST 1 second before the `slow: success` line — the
#    fast instance's terminal arrived during the slow instance's
#    delay, not buffered to the end;
#  - the operator-visible cadence is bounded by the verb's
#    DefaultWaitPollInterval (≤1s); the test allows up to a 2s slop
#    above the 3s delay to admit polling jitter without false fails.

set -u

repo_root() {
  cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd
}

die() {
  echo "live-progress-demo: $*" >&2
  exit 1
}

step() {
  echo "==> $*"
}

REPO="$(repo_root)"
WORK="$(mktemp -d -t rimsky-live-demo-XXXXXX)"
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

TRANSCRIPT="$WORK/run.transcript"
step "run rimsky compose run; pipe stderr through a per-line timestamper"

# @deliberate: the per-line timestamper reads stderr from the verb and
# prefixes each line with a wall-clock epoch (seconds.nanoseconds). The
# verb's stderr is line-buffered through its slog handler (the default
# printer flushes after every line; see progress.go); the timestamper
# tags each line as it arrives, so the resulting transcript pins the
# arrival time of every observable lifecycle event.
#
# We deliberately do NOT use `awk '{print strftime(...)...}'` here:
# strftime's resolution is per-second and the slow vs fast separation
# is in the seconds-range; date with %s.%N gives nanosecond resolution
# so the ordering assertion is unambiguous.
#
# `script -q /dev/null` (macOS/FreeBSD) or `stdbuf -oL -eL` (Linux)
# would each put a pty / line-buffering wrapper on the verb's stderr
# so its line-buffered writes do not get block-buffered when piped
# through another process. We prefer plain `bash` composition by
# routing through a `while read` loop, which forces per-line emission
# without requiring either.
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

# @constraint: falsifier #1 — both `fast: success` and `slow: success`
# lines must be present in the transcript, proving the verb ran the
# wait loop through to both instances' terminals.
fast_line=$(grep -E "instance live-pipeline/fast: success" "$TRANSCRIPT" | head -n 1 || true)
slow_line=$(grep -E "instance live-pipeline/slow: success" "$TRANSCRIPT" | head -n 1 || true)
[[ -n "$fast_line" ]] || die "FAIL: 'live-pipeline/fast: success' missing from transcript"
[[ -n "$slow_line" ]] || die "FAIL: 'live-pipeline/slow: success' missing from transcript"

fast_ts=$(printf '%s' "$fast_line" | awk '{print $1}')
slow_ts=$(printf '%s' "$slow_line" | awk '{print $1}')
[[ -n "$fast_ts" && -n "$slow_ts" ]] || die "FAIL: transcript lines missing timestamp prefix"

# @constraint: falsifier #2 — the fast instance's terminal line must
# arrive at least 1 second BEFORE the slow instance's terminal line. If
# both appeared at roughly the same wall-clock time, the verb buffered
# all lifecycle events to the end of the run, exactly the failure mode
# the story rules out.
delta=$(awk -v a="$slow_ts" -v b="$fast_ts" 'BEGIN { printf "%.3f", a - b }')
step "fast terminal at $fast_ts; slow terminal at $slow_ts; delta=${delta}s"

# @deliberate: bash's [[ ... -gt ... ]] is integer-only; compare via
# awk to admit a fractional threshold.
ok=$(awk -v d="$delta" 'BEGIN { print (d >= 1.0) ? "yes" : "no" }')
[[ "$ok" == "yes" ]] \
  || die "FAIL: fast terminal arrived within ${delta}s of slow terminal (expected ≥1.0s gap) — verb appears to be buffering progress lines"

# @constraint: falsifier #3 (sanity) — the delta should also be
# bounded above; anything beyond 6s suggests the slow path took much
# longer than its delay_ms attribute (3s + 2s slop + 1s wait-poll
# margin). A blown-up delta would mean the slow path didn't actually
# start until after the fast one finished — a serialized execution
# shape we want to rule out.
sane=$(awk -v d="$delta" 'BEGIN { print (d <= 6.0) ? "yes" : "no" }')
[[ "$sane" == "yes" ]] \
  || die "FAIL: delta=${delta}s is greater than the expected 6s upper bound; instances may be serializing rather than running concurrently"

step "PASS"
echo "STORY-live-progress proven:"
echo "  - the fast instance's terminal line arrived ${delta}s before the slow instance's"
echo "  - the transcript timestamps prove progress is emitted live, not batched"
exit 0
