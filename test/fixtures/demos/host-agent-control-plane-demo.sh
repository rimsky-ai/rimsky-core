#!/usr/bin/env bash
# Copyright © 2026 Fall Guy Consulting.
# SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

# @story: host-agent-control-plane
set -euo pipefail

RIMSKY_BIN="${RIMSKY_BIN:-rimsky}"
RIMSKY_PROXY_BIN="${RIMSKY_PROXY_BIN:-rimsky-host-agent-proxy}"
RIMSKY_MIGRATE_BIN="${RIMSKY_MIGRATE_BIN:-rimsky-migrate}"
RIMSKY_CONTROL_API_BIN="${RIMSKY_CONTROL_API_BIN:-rimsky-control-api}"

STATE_DIR="$( mktemp -d -t rimsky-agent-demo.XXXXXXXX )"

BOGUS_PROXY="rimsky-agent-demo-bogus.invalid:65535"

PROXY_PID=""
CONTROL_PID=""
cleanup() {
    local rc=$?
    if [ -n "${PROXY_PID}" ] && kill -0 "${PROXY_PID}" 2>/dev/null; then
        kill "${PROXY_PID}" 2>/dev/null || true
        wait "${PROXY_PID}" 2>/dev/null || true
    fi
    if [ -n "${CONTROL_PID}" ] && kill -0 "${CONTROL_PID}" 2>/dev/null; then
        kill "${CONTROL_PID}" 2>/dev/null || true
        wait "${CONTROL_PID}" 2>/dev/null || true
    fi
    "${RIMSKY_BIN}" agent stop --state-dir "${STATE_DIR}" >/dev/null 2>&1 || true
    rm -rf "${STATE_DIR}"
    exit "${rc}"
}
trap cleanup EXIT INT TERM

pick_free_port() {
    python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()'
}

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

echo "host-agent-control-plane-demo: step 1 — agent start against bogus proxy (expect failure)"

set +e
FAIL_STDERR="$( "${RIMSKY_BIN}" agent start \
    --proxy "${BOGUS_PROXY}" \
    --state-dir "${STATE_DIR}" 2>&1 >/dev/null )"
FAIL_RC=$?
set -e

if [ "${FAIL_RC}" -eq 0 ]; then
    echo "host-agent-control-plane-demo: FAIL — start against ${BOGUS_PROXY} returned exit 0 (expected non-zero)" >&2
    echo "host-agent-control-plane-demo: stderr was:" >&2
    echo "${FAIL_STDERR}" >&2
    exit 1
fi

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

if [ -f "${STATE_DIR}/agent.pid" ]; then
    echo "host-agent-control-plane-demo: FAIL — failed start left a stale pid file at ${STATE_DIR}/agent.pid" >&2
    exit 1
fi
echo "host-agent-control-plane-demo: step 1 OK — failure path refused cleanly (rc=${FAIL_RC})"

CONTROL_PORT="$( pick_free_port )"
CONTROL_ADDR="127.0.0.1:${CONTROL_PORT}"
CONTROL_URL="http://${CONTROL_ADDR}"
RIMSKY_CONFIG_PATH="${STATE_DIR}/rimsky.yml"

cat > "${RIMSKY_CONFIG_PATH}" <<EOF
persistence:
  driver: sqlite
  sqlite:
    path: ${STATE_DIR}/state.db

claim_producers: {}
named_locks: {}
executors: {}
EOF

echo "host-agent-control-plane-demo: step 2 — booting control plane (anonymous mode) on ${CONTROL_ADDR}"

RIMSKY_CONFIG="${RIMSKY_CONFIG_PATH}" "${RIMSKY_MIGRATE_BIN}" >/dev/null

RIMSKY_CONFIG="${RIMSKY_CONFIG_PATH}" \
RIMSKY_CONTROL_API_PORT="${CONTROL_PORT}" \
RIMSKY_LOG_LEVEL=warn \
"${RIMSKY_CONTROL_API_BIN}" >/dev/null 2>&1 &
CONTROL_PID=$!

if ! wait_dialable "${CONTROL_ADDR}" 10; then
    echo "host-agent-control-plane-demo: FAIL — control api did not come up on ${CONTROL_ADDR} within 10s" >&2
    exit 1
fi

PROXY_PORT="$( pick_free_port )"
PROXY_ADDR="127.0.0.1:${PROXY_PORT}"

echo "host-agent-control-plane-demo: step 3 — booting ${RIMSKY_PROXY_BIN} on ${PROXY_ADDR}"

RIMSKY_PROXY_GRPC_PORT="${PROXY_PORT}" \
RIMSKY_CONTROL_API_URL="${CONTROL_URL}" \
RIMSKY_LOG_LEVEL=warn \
"${RIMSKY_PROXY_BIN}" >/dev/null 2>&1 &
PROXY_PID=$!

if ! wait_dialable "${PROXY_ADDR}" 10; then
    echo "host-agent-control-plane-demo: FAIL — proxy did not come up on ${PROXY_ADDR} within 10s" >&2
    exit 1
fi

echo "host-agent-control-plane-demo: step 4 — agent start --proxy ${PROXY_ADDR} (anonymous mode, no api-key)"

START_STDOUT="$( "${RIMSKY_BIN}" agent start \
    --proxy "${PROXY_ADDR}" \
    --state-dir "${STATE_DIR}" )"
echo "${START_STDOUT}"

case "${START_STDOUT}" in
    *"connected to ${PROXY_ADDR}"*) ;;
    *) echo "host-agent-control-plane-demo: FAIL — start did not report 'connected to ${PROXY_ADDR}'" >&2
       exit 1 ;;
esac

AGENT_PID="$( cat "${STATE_DIR}/agent.pid" )"
echo "host-agent-control-plane-demo: agent pid ${AGENT_PID}"

echo "host-agent-control-plane-demo: step 5 — agent status (expect connected)"

STATUS_STDOUT="$( "${RIMSKY_BIN}" agent status --state-dir "${STATE_DIR}" )"
echo "${STATUS_STDOUT}"

case "${STATUS_STDOUT}" in
    *"connected"*) ;;
    *) echo "host-agent-control-plane-demo: FAIL — status did not report 'connected'" >&2
       exit 1 ;;
esac

echo "host-agent-control-plane-demo: step 6 — agent stop (expect clean exit, no zombies)"

STOP_STDOUT="$( "${RIMSKY_BIN}" agent stop --state-dir "${STATE_DIR}" )"
echo "${STOP_STDOUT}"

case "${STOP_STDOUT}" in
    *"stopped (pid ${AGENT_PID})"*) ;;
    *) echo "host-agent-control-plane-demo: FAIL — stop did not report the agent stopped" >&2
       exit 1 ;;
esac

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

POST_STOP_STATUS="$( "${RIMSKY_BIN}" agent status --state-dir "${STATE_DIR}" )"
case "${POST_STOP_STATUS}" in
    *"not running"*) ;;
    *) echo "host-agent-control-plane-demo: FAIL — post-stop status was %s, expected 'not running'" >&2
       echo "${POST_STOP_STATUS}" >&2
       exit 1 ;;
esac

echo "host-agent-control-plane-demo: all steps OK — start/status/stop lifecycle is sound"
