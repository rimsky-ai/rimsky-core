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
[[ "$rc" == "1" ]] || die "FAIL: verb exited $rc; expected 1 (any-failure for mixed-outcome manifest)"

step "open the audit artifact"
LATEST="$(readlink .rimsky/latest)"
[[ -n "$LATEST" ]] || die "FAIL: .rimsky/latest symlink missing or empty"
RUN_DIR="$WORK/.rimsky/$LATEST"
[[ -d "$RUN_DIR" ]] || die "FAIL: latest symlink target $RUN_DIR is not a directory"

echo "  per-run dir: $RUN_DIR"
echo "  contents:"
ls -la "$RUN_DIR"

[[ -f "$RUN_DIR/state.db" ]] || die "FAIL: $RUN_DIR/state.db missing"
[[ -d "$RUN_DIR/blobs" ]] || die "FAIL: $RUN_DIR/blobs/ missing"

step "query state.db with the stock sqlite3 CLI"

echo "  -- rimsky_instances --"
sqlite3 "$RUN_DIR/state.db" \
  "SELECT id, instance_key, terminated_at IS NOT NULL AS terminated FROM rimsky_instances ORDER BY instance_key;"

INSTANCE_COUNT=$(sqlite3 "$RUN_DIR/state.db" \
  "SELECT COUNT(*) FROM rimsky_instances;")
[[ "$INSTANCE_COUNT" == "2" ]] \
  || die "FAIL: expected 2 instance rows; got $INSTANCE_COUNT"

echo "  -- rimsky_node_runs (count by phase) --"
sqlite3 "$RUN_DIR/state.db" \
  "SELECT phase, COUNT(*) FROM rimsky_node_runs GROUP BY phase;"

NODE_RUN_COUNT=$(sqlite3 "$RUN_DIR/state.db" \
  "SELECT COUNT(*) FROM rimsky_node_runs;")
(( NODE_RUN_COUNT >= 2 )) \
  || die "FAIL: expected at least 2 node-run rows (one per worker dispatch); got $NODE_RUN_COUNT"

echo "  -- rimsky_nodes (worker rows) --"
sqlite3 "$RUN_DIR/state.db" \
  "SELECT id, node_type, executor FROM rimsky_nodes WHERE node_type = 'worker' ORDER BY id LIMIT 5;"

WORKER_COUNT=$(sqlite3 "$RUN_DIR/state.db" \
  "SELECT COUNT(*) FROM rimsky_nodes WHERE node_type = 'worker';")
(( WORKER_COUNT >= 2 )) \
  || die "FAIL: expected at least 2 worker nodes recorded by name; got $WORKER_COUNT"

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
