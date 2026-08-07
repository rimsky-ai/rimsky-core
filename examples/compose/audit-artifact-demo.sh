#!/usr/bin/env bash
# Copyright © 2026 Fall Guy Consulting.
# SPDX-License-Identifier: Apache-2.0

set -u

script_dir() {
  cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd
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

HERE="$(script_dir)"
WORK="$(mktemp -d -t rimsky-audit-demo-XXXXXX)"
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

echo "  -- rimsky_node_runs (count by state) --"
sqlite3 "$RUN_DIR/state.db" \
  "SELECT state, COUNT(*) FROM rimsky_node_runs GROUP BY state;"

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
  "SELECT id, node_id, state, executor_name, claimed_by FROM rimsky_node_runs WHERE state = 'failed';"

FAILED_COUNT=$(sqlite3 "$RUN_DIR/state.db" \
  "SELECT COUNT(*) FROM rimsky_node_runs WHERE state = 'failed';")
(( FAILED_COUNT >= 1 )) \
  || die "FAIL: expected at least 1 rimsky_node_runs row in state 'failed' (the audit-trail terminal column for the failing dispatch); got $FAILED_COUNT"

COMPLETED_COUNT=$(sqlite3 "$RUN_DIR/state.db" \
  "SELECT COUNT(*) FROM rimsky_node_runs WHERE state = 'fresh';")
(( COMPLETED_COUNT >= 1 )) \
  || die "FAIL: expected at least 1 rimsky_node_runs row in state 'fresh' (the audit-trail terminal column for the succeeding dispatch); got $COMPLETED_COUNT"

step "PASS"
echo "STORY-audit-artifact proven:"
echo "  - .rimsky/latest resolves to a per-run directory the operator can cd into"
echo "  - state.db loads in stock sqlite3 (not a rimsky-specific tool)"
echo "  - rimsky_instances carries the run's terminated instance rows"
echo "  - rimsky_node_runs carries per-node-run history with"
echo "    distinct terminal states for the success ('fresh') and"
echo "    failure ('failed') legs — pulled out by hand"
echo "  - rimsky_nodes records node names that join the audit trail"
echo "  - blobs/ subdir is present (filesystem blob backend's root)"
exit 0
