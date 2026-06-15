#!/usr/bin/env bash
# Copyright © 2026 Fall Guy Consulting.
# Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
# license. See LICENSE.agpl and COPYRIGHT at the repo root.
#
# audit-artifact-demo.sh — STORY-audit-artifact proof.
#
# Role: operator who wants to reach into the durable record of a run
# after the verb returns and reconstruct what happened — instance
# terminations, node-run history, attributes, event log — using
# widely-available tooling, not rimsky-specific ones.
#
# What this demo exhibits, by Falsifier:
#  - the per-run artifact survives the process exit and lives at a
#    stable, discoverable location (the .rimsky/latest symlink + the
#    timestamped per-run directory under .rimsky/runs/);
#  - the artifact loads in `sqlite3` — a widely-available CLI, NOT a
#    rimsky-specific tool — and the operator can query both
#    instance-level state AND per-node-run history out of it;
#  - the run-directory layout includes the blobs/ subdirectory the
#    spec's @decision: artifact-layout mandates;
#  - the per-node-run row for the failing node carries phase='failed'
#    — the operator-readable terminal class column the post-mortem
#    walkthrough pulls out by hand.
#
# This demo drives the mixed-outcome manifest from
# STORY-one-shot-to-terminal: one instance succeeds, one fails. The
# audit-artifact story's Proof field literally requires "drive a small
# failing manifest, then walk through opening the artifact and pulling
# the failing node-run's terminal event out by hand"; the post-mortem
# query below executes that walkthrough against
# rimsky_node_runs.phase='failed'.

set -u

repo_root() {
  cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd
}

die() {
  echo "audit-artifact-demo: $*" >&2
  exit 1
}

step() {
  echo "==> $*"
}

# @constraint: sqlite3 is required for the post-mortem walkthrough —
# the story's point is that the artifact is in a widely-available
# format, queryable without rimsky-specific tooling, so a fixture pre-
# flight checks for sqlite3 on PATH and bails with a clear message if
# it is missing.
command -v sqlite3 >/dev/null 2>&1 \
  || die "sqlite3 binary not found on PATH; install sqlite3 or set PATH to include it"

REPO="$(repo_root)"
WORK="$(mktemp -d -t rimsky-audit-demo-XXXXXX)"
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

step "verb exited with code: $rc"
# @constraint: mixed-outcome manifest → any-failure → exit 1. Anything
# else means the wait loop did not classify the per-instance roster
# correctly.
[[ "$rc" == "1" ]] || die "FAIL: verb exited $rc; expected 1 (any-failure for mixed-outcome manifest)"

# @deliberate: post-mortem walkthrough — the operator points at the run
# directory via .rimsky/latest (the symlink the verb updates after
# apply), inspects the per-run files on disk, then opens state.db with
# the stock sqlite3 binary and pulls instance + node-run rows by hand.
step "open the audit artifact"
LATEST="$(readlink .rimsky/latest)"
[[ -n "$LATEST" ]] || die "FAIL: .rimsky/latest symlink missing or empty"
RUN_DIR="$WORK/.rimsky/$LATEST"
[[ -d "$RUN_DIR" ]] || die "FAIL: latest symlink target $RUN_DIR is not a directory"

echo "  per-run dir: $RUN_DIR"
echo "  contents:"
ls -la "$RUN_DIR"

# @constraint: falsifier #1 — the run directory must contain state.db
# AND a blobs/ subdir. Either one missing is a structural artifact-
# layout failure.
[[ -f "$RUN_DIR/state.db" ]] || die "FAIL: $RUN_DIR/state.db missing"
[[ -d "$RUN_DIR/blobs" ]] || die "FAIL: $RUN_DIR/blobs/ missing"

step "query state.db with the stock sqlite3 CLI"

# @constraint: falsifier #2a — instance rows must be present,
# addressable by the manifest-supplied instance keys, both terminated.
echo "  -- rimsky_instances --"
sqlite3 "$RUN_DIR/state.db" \
  "SELECT id, instance_key, terminated_at IS NOT NULL AS terminated FROM rimsky_instances ORDER BY instance_key;"

INSTANCE_COUNT=$(sqlite3 "$RUN_DIR/state.db" \
  "SELECT COUNT(*) FROM rimsky_instances;")
[[ "$INSTANCE_COUNT" == "2" ]] \
  || die "FAIL: expected 2 instance rows; got $INSTANCE_COUNT"

# @constraint: falsifier #2b — per-node-run history must be present.
# The story's Falsifier rules out "only state metadata (last-known
# status flags) without per-node-run history". The rimsky_node_runs
# table carries the per-dispatch settled rows; a zero count means the
# artifact captured the instance lifecycle but not the per-run history.
echo "  -- rimsky_node_runs (count by phase) --"
sqlite3 "$RUN_DIR/state.db" \
  "SELECT phase, COUNT(*) FROM rimsky_node_runs GROUP BY phase;"

NODE_RUN_COUNT=$(sqlite3 "$RUN_DIR/state.db" \
  "SELECT COUNT(*) FROM rimsky_node_runs;")
(( NODE_RUN_COUNT >= 2 )) \
  || die "FAIL: expected at least 2 node-run rows (one per worker dispatch); got $NODE_RUN_COUNT"

# @constraint: falsifier #2c — per-node names are recorded; the
# operator can follow the manifest's node type to a per-node-run row.
echo "  -- rimsky_nodes (worker rows) --"
sqlite3 "$RUN_DIR/state.db" \
  "SELECT id, node_type, executor FROM rimsky_nodes WHERE node_type = 'worker' ORDER BY id LIMIT 5;"

WORKER_COUNT=$(sqlite3 "$RUN_DIR/state.db" \
  "SELECT COUNT(*) FROM rimsky_nodes WHERE node_type = 'worker';")
(( WORKER_COUNT >= 2 )) \
  || die "FAIL: expected at least 2 worker nodes recorded by name; got $WORKER_COUNT"

# @constraint: falsifier #2d — pull the failing node-run's terminal
# class out by hand, the literal post-mortem walkthrough the story's
# Proof field requires. rimsky_node_runs.phase is the operator-facing
# terminal label per dispatch (CHECK pending|active|held|parked|
# completed|failed). The mixed-outcome manifest lands one row in
# phase='failed' (the `oops` instance's worker, error_class
# 'stub/failed' from the stub executor) and one in phase='completed'
# (the `ok` instance's worker). Operator selects the failing row
# directly:
echo "  -- the failing node-run, pulled out by hand --"
sqlite3 "$RUN_DIR/state.db" \
  "SELECT id, node_id, phase, executor_name, claimed_by FROM rimsky_node_runs WHERE phase = 'failed';"

FAILED_COUNT=$(sqlite3 "$RUN_DIR/state.db" \
  "SELECT COUNT(*) FROM rimsky_node_runs WHERE phase = 'failed';")
(( FAILED_COUNT >= 1 )) \
  || die "FAIL: expected at least 1 rimsky_node_runs row in phase 'failed' (the audit-trail terminal column for the failing dispatch); got $FAILED_COUNT"

COMPLETED_COUNT=$(sqlite3 "$RUN_DIR/state.db" \
  "SELECT COUNT(*) FROM rimsky_node_runs WHERE phase = 'completed';")
(( COMPLETED_COUNT >= 1 )) \
  || die "FAIL: expected at least 1 rimsky_node_runs row in phase 'completed' (the audit-trail terminal column for the succeeding dispatch); got $COMPLETED_COUNT"

step "PASS"
echo "STORY-audit-artifact proven:"
echo "  - .rimsky/latest resolves to a per-run directory the operator can cd into"
echo "  - state.db loads in stock sqlite3 (not a rimsky-specific tool)"
echo "  - rimsky_instances carries the run's terminated instance rows"
echo "  - rimsky_node_runs carries per-node-run history with"
echo "    distinct terminal phases for the success ('completed') and"
echo "    failure ('failed') legs — pulled out by hand"
echo "  - rimsky_nodes records node names that join the audit trail"
echo "  - blobs/ subdir is present (filesystem blob backend's root)"
exit 0
