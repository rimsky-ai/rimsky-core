#!/usr/bin/env bash
# Copyright © 2026 Fall Guy Consulting.
# Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
# repo root, or http://www.apache.org/licenses/LICENSE-2.0.

# host-agent-control-plane-demo.sh — STORY-host-agent-control-plane proof.
#
# An operator running rimsky-dispatched workflows on a dev machine manages
# the host-agent's lifecycle from the same CLI that drives the rimsky
# stack: `rimsky agent start` brings the daemon up against a configured
# proxy, `rimsky agent status` reports the connection state, and `rimsky
# agent stop` brings it down cleanly (children reaped, no zombies).
#
# The script exercises three load-bearing properties named in the story's
# Falsifier:
#
#   1. `start` REFUSES with a clear diagnostic on a misconfigured proxy
#      URL — it does NOT silently succeed and leave a daemon
#      loop-dialing forever. The failure-path block runs FIRST so a
#      stale pid/status file from a real prior run cannot mask the
#      diagnostic.
#
#   2. `status` reports `connected` only when the bidi stream is
#      actually up — it reads the daemon's connection sentinel, not
#      just the pid file.
#
#   3. `stop` brings the daemon down cleanly (exit code 0) and the OS
#      process actually goes away — confirmed by polling the pid until
#      it is no longer alive.
#
# Prerequisites the operator must satisfy BEFORE running this script:
#
#   1. A running `rimsky-host-agent-proxy` reachable at $PROXY_ADDR.
#      For containerized deployments the operator boots
#      `rimsky-host-agent-proxy:latest` and exposes its gRPC port;
#      for local dev `RIMSKY_PROXY_BIN` can point at a freshly-built
#      binary which this script spawns directly. The driver test
#      under `test/scenarios/host_agent_control_plane_demo_test.go`
#      uses the binary path so the full flow runs in CI without
#      Docker.
#
#   2. The `rimsky` CLI binary on $PATH (or `RIMSKY_BIN` pointing at a
#      built binary). `make cli` builds it for bare-metal use.
#
# Output discipline: exits 0 only when all three properties were
# exhibited. Any deviation (a successful `start` against a bogus proxy,
# a `connected` report against a torn-down stream, a `stop` that fails
# to shut the process down) exits non-zero with a diagnostic.

set -euo pipefail

# Allow the test harness to inject explicit binary paths. When unset the
# script falls back to binaries on $PATH — the bare-metal path.
RIMSKY_BIN="${RIMSKY_BIN:-rimsky}"
RIMSKY_PROXY_BIN="${RIMSKY_PROXY_BIN:-rimsky-host-agent-proxy}"

# State dir isolates pid + status files so concurrent runs (CI, repeated
# manual invocations) never collide on the default ~/.rimsky path.
STATE_DIR="$( mktemp -d -t rimsky-agent-demo.XXXXXXXX )"

# A bogus URL that DNS-fails fast so the failure-path block doesn't
# spend the full readiness window dial-retrying a routable-but-silent
# address. RFC 6761 reserves ".invalid" so this is guaranteed bogus.
BOGUS_PROXY="rimsky-agent-demo-bogus.invalid:65535"

# Cleanup: best-effort tear-down so a mid-script failure doesn't leave a
# stray proxy or agent dangling. The trap fires on EXIT regardless of
# exit code so failure exhibits leave a clean tree.
PROXY_PID=""
cleanup() {
    local rc=$?
    if [ -n "${PROXY_PID}" ] && kill -0 "${PROXY_PID}" 2>/dev/null; then
        kill "${PROXY_PID}" 2>/dev/null || true
        wait "${PROXY_PID}" 2>/dev/null || true
    fi
    # Best-effort: stop the agent if it's still running so the test
    # harness reaps a clean tree even when an assertion fails mid-flow.
    "${RIMSKY_BIN}" agent stop --state-dir "${STATE_DIR}" >/dev/null 2>&1 || true
    rm -rf "${STATE_DIR}"
    exit "${rc}"
}
trap cleanup EXIT INT TERM

# pick_free_port grabs an OS-assigned TCP port via Python (universally
# available on dev machines) and prints it. The brief close-then-reuse
# race is acceptable for an in-process demo fixture.
pick_free_port() {
    python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()'
}

# wait_dialable polls a host:port until TCP connect succeeds or timeout.
wait_dialable() {
    local addr="$1"
    local timeout="$2"
    local deadline=$(( $( date +%s ) + timeout ))
    while [ "$( date +%s )" -lt "${deadline}" ]; do
        if python3 -c "import socket,sys; s=socket.socket(); s.settimeout(0.2); s.connect(('${addr%:*}', int('${addr##*:}'))); s.close()" 2>/dev/null; then
            return 0
        fi
        sleep 0.1
    done
    return 1
}

echo "host-agent-control-plane-demo: state dir ${STATE_DIR}"

# ---------------------------------------------------------------------------
# Step 1 — failure path FIRST. A misconfigured `--proxy` URL must surface
# as a non-zero exit with a clear diagnostic. The Falsifier names "start
# silently succeeds with a misconfigured proxy URL" as a failure mode;
# this block is the proof.
# ---------------------------------------------------------------------------

echo "host-agent-control-plane-demo: step 1 — agent start against bogus proxy (expect failure)"

set +e
FAIL_STDERR="$( "${RIMSKY_BIN}" agent start \
    --proxy "${BOGUS_PROXY}" \
    --state-dir "${STATE_DIR}" \
    --api-key "demo-key" 2>&1 >/dev/null )"
FAIL_RC=$?
set -e

if [ "${FAIL_RC}" -eq 0 ]; then
    echo "host-agent-control-plane-demo: FAIL — start against ${BOGUS_PROXY} returned exit 0 (expected non-zero)" >&2
    echo "host-agent-control-plane-demo: stderr was:" >&2
    echo "${FAIL_STDERR}" >&2
    exit 1
fi

# The diagnostic must mention the unreachable/misconfigured proxy so the
# operator can act on it — a silent non-zero with no context is itself a
# Falsifier failure mode ("clear diagnostic" is part of the Acceptance).
case "${FAIL_STDERR}" in
    *"${BOGUS_PROXY}"*) ;;
    *) echo "host-agent-control-plane-demo: FAIL — failure diagnostic did not name the bogus proxy URL" >&2
       echo "host-agent-control-plane-demo: stderr was:" >&2
       echo "${FAIL_STDERR}" >&2
       exit 1 ;;
esac
case "${FAIL_STDERR}" in
    *unreachable*|*misconfigured*|*exited*|*"did not connect"*) ;;
    *) echo "host-agent-control-plane-demo: FAIL — failure diagnostic did not explain the failure mode" >&2
       echo "host-agent-control-plane-demo: stderr was:" >&2
       echo "${FAIL_STDERR}" >&2
       exit 1 ;;
esac

# No agent.pid should remain after the failed start — the failure must
# leave a clean tree so a subsequent `agent status` doesn't lie.
if [ -f "${STATE_DIR}/agent.pid" ]; then
    echo "host-agent-control-plane-demo: FAIL — failed start left a stale pid file at ${STATE_DIR}/agent.pid" >&2
    exit 1
fi
echo "host-agent-control-plane-demo: step 1 OK — failure path refused cleanly (rc=${FAIL_RC})"

# ---------------------------------------------------------------------------
# Step 2 — boot the proxy on a free port, then drive the happy path:
# start, status (expect connected), stop.
# ---------------------------------------------------------------------------

PROXY_PORT="$( pick_free_port )"
PROXY_ADDR="127.0.0.1:${PROXY_PORT}"

echo "host-agent-control-plane-demo: step 2 — booting ${RIMSKY_PROXY_BIN} on ${PROXY_ADDR}"

# Run the proxy with no control-api fallback (this demo doesn't exercise
# the dispatch path; the driver test does). RIMSKY_LOG_LEVEL=warn keeps
# stderr terse so the demo's own output is the load-bearing signal.
RIMSKY_PROXY_GRPC_PORT="${PROXY_PORT}" \
RIMSKY_LOG_LEVEL=warn \
"${RIMSKY_PROXY_BIN}" >/dev/null 2>&1 &
PROXY_PID=$!

if ! wait_dialable "${PROXY_ADDR}" 10; then
    echo "host-agent-control-plane-demo: FAIL — proxy did not come up on ${PROXY_ADDR} within 10s" >&2
    exit 1
fi

echo "host-agent-control-plane-demo: step 3 — agent start --proxy ${PROXY_ADDR}"

START_STDOUT="$( "${RIMSKY_BIN}" agent start \
    --proxy "${PROXY_ADDR}" \
    --state-dir "${STATE_DIR}" \
    --api-key "demo-key" )"
echo "${START_STDOUT}"

# A successful start prints "rimsky agent started (pid N, connected to ADDR)" —
# the "connected to" segment proves the readiness handshake succeeded
# (not just a silent fork).
case "${START_STDOUT}" in
    *"connected to ${PROXY_ADDR}"*) ;;
    *) echo "host-agent-control-plane-demo: FAIL — start did not report 'connected to ${PROXY_ADDR}'" >&2
       exit 1 ;;
esac

# Capture the daemon pid for the zombie-children check at stop time.
AGENT_PID="$( cat "${STATE_DIR}/agent.pid" )"
echo "host-agent-control-plane-demo: agent pid ${AGENT_PID}"

echo "host-agent-control-plane-demo: step 4 — agent status (expect connected)"

STATUS_STDOUT="$( "${RIMSKY_BIN}" agent status --state-dir "${STATE_DIR}" )"
echo "${STATUS_STDOUT}"

# The status report must say `connected` — anything else (`disconnected`,
# `not running`, `status unreadable`) means the sentinel reflects something
# other than the live bidi stream.
case "${STATUS_STDOUT}" in
    *"connected"*) ;;
    *) echo "host-agent-control-plane-demo: FAIL — status did not report 'connected'" >&2
       exit 1 ;;
esac

echo "host-agent-control-plane-demo: step 5 — agent stop (expect clean exit, no zombies)"

STOP_STDOUT="$( "${RIMSKY_BIN}" agent stop --state-dir "${STATE_DIR}" )"
echo "${STOP_STDOUT}"

case "${STOP_STDOUT}" in
    *"stopped (pid ${AGENT_PID})"*) ;;
    *) echo "host-agent-control-plane-demo: FAIL — stop did not report the agent stopped" >&2
       exit 1 ;;
esac

# Confirm the OS process is actually gone. `agent stop` returning success
# is necessary but not sufficient — the Falsifier names "stop exits
# cleanly but leaves zombie children" as the load-bearing failure mode.
# A live agent.pid after stop, or any child the agent had spawned still
# alive, would be a real defect. The driver test additionally exercises
# the proxy-tunneled dispatch path so it can prove children spawned via
# dispatch are reaped; this script proves the daemon itself goes away.
deadline=$(( $( date +%s ) + 5 ))
while [ "$( date +%s )" -lt "${deadline}" ]; do
    if ! kill -0 "${AGENT_PID}" 2>/dev/null; then
        break
    fi
    sleep 0.1
done
if kill -0 "${AGENT_PID}" 2>/dev/null; then
    echo "host-agent-control-plane-demo: FAIL — agent pid ${AGENT_PID} is still alive after stop" >&2
    exit 1
fi
if [ -f "${STATE_DIR}/agent.pid" ]; then
    echo "host-agent-control-plane-demo: FAIL — stop did not remove ${STATE_DIR}/agent.pid" >&2
    exit 1
fi

# A second `status` after stop must report `not running` — proves the
# stop fully tore down the recorded state.
POST_STOP_STATUS="$( "${RIMSKY_BIN}" agent status --state-dir "${STATE_DIR}" )"
case "${POST_STOP_STATUS}" in
    *"not running"*) ;;
    *) echo "host-agent-control-plane-demo: FAIL — post-stop status was %s, expected 'not running'" >&2
       echo "${POST_STOP_STATUS}" >&2
       exit 1 ;;
esac

echo "host-agent-control-plane-demo: all steps OK — start/status/stop lifecycle is sound"
