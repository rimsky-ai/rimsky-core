#!/usr/bin/env bash
# Copyright © 2026 Fall Guy Consulting.
# SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

# @story: host-daemon-control-plane
set -euo pipefail

RIMSKY_BIN="${RIMSKY_BIN:-rimsky}"
RIMSKY_PROXY_BIN="${RIMSKY_PROXY_BIN:-rimsky-host-daemon-proxy}"
RIMSKY_MIGRATE_BIN="${RIMSKY_MIGRATE_BIN:-rimsky-migrate}"
RIMSKY_CONTROL_API_BIN="${RIMSKY_CONTROL_API_BIN:-rimsky-control-api}"

STATE_DIR="$( mktemp -d -t rimsky-daemon-demo.XXXXXXXX )"

BOGUS_PROXY="rimsky-daemon-demo-bogus.invalid:65535"

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
    "${RIMSKY_BIN}" daemon stop --state-dir "${STATE_DIR}" >/dev/null 2>&1 || true
    rm -rf "${STATE_DIR}"
    exit "${rc}"
}
trap cleanup EXIT INT TERM

pick_free_port() {
    python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()'
}

poll_until_dialable_or_process_exits() {
    local addr="$1"
    local pid="$2"
    while true; do
        if python3 -c "import socket,sys; s=socket.socket(); s.settimeout(0.2); s.connect(('${addr%:*}', int('${addr##*:}'))); s.close()" 2>/dev/null; then
            return 0
        fi
        if ! kill -0 "${pid}" 2>/dev/null; then
            return 1
        fi
        sleep 0.1
    done
}

echo "host-daemon-control-plane-demo: state dir ${STATE_DIR}"

echo "host-daemon-control-plane-demo: step 1 — daemon start against bogus proxy (expect failure)"

set +e
FAIL_STDERR="$( "${RIMSKY_BIN}" daemon start \
    --proxy "${BOGUS_PROXY}" \
    --state-dir "${STATE_DIR}" 2>&1 >/dev/null )"
FAIL_RC=$?
set -e

if [ "${FAIL_RC}" -eq 0 ]; then
    echo "host-daemon-control-plane-demo: FAIL — start against ${BOGUS_PROXY} returned exit 0 (expected non-zero)" >&2
    echo "host-daemon-control-plane-demo: stderr was:" >&2
    echo "${FAIL_STDERR}" >&2
    exit 1
fi

case "${FAIL_STDERR}" in
    *"${BOGUS_PROXY}"*) ;;
    *) echo "host-daemon-control-plane-demo: FAIL — failure diagnostic did not name the bogus proxy URL" >&2
       echo "host-daemon-control-plane-demo: stderr was:" >&2
       echo "${FAIL_STDERR}" >&2
       exit 1 ;;
esac
case "${FAIL_STDERR}" in
    *unreachable*|*misconfigured*|*exited*|*"did not connect"*) ;;
    *) echo "host-daemon-control-plane-demo: FAIL — failure diagnostic did not explain the failure mode" >&2
       echo "host-daemon-control-plane-demo: stderr was:" >&2
       echo "${FAIL_STDERR}" >&2
       exit 1 ;;
esac

if [ -f "${STATE_DIR}/daemon.pid" ]; then
    echo "host-daemon-control-plane-demo: FAIL — failed start left a stale pid file at ${STATE_DIR}/daemon.pid" >&2
    exit 1
fi
echo "host-daemon-control-plane-demo: step 1 OK — failure path refused cleanly (rc=${FAIL_RC})"

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

echo "host-daemon-control-plane-demo: step 2 — booting control plane (anonymous mode) on ${CONTROL_ADDR}"

RIMSKY_CONFIG="${RIMSKY_CONFIG_PATH}" "${RIMSKY_MIGRATE_BIN}" >/dev/null

RIMSKY_CONFIG="${RIMSKY_CONFIG_PATH}" \
RIMSKY_CONTROL_API_PORT="${CONTROL_PORT}" \
RIMSKY_LOG_LEVEL=warn \
"${RIMSKY_CONTROL_API_BIN}" >/dev/null 2>&1 &
CONTROL_PID=$!

if ! poll_until_dialable_or_process_exits "${CONTROL_ADDR}" "${CONTROL_PID}"; then
    echo "host-daemon-control-plane-demo: FAIL — control api exited before ${CONTROL_ADDR} answered" >&2
    exit 1
fi

PROXY_PORT="$( pick_free_port )"
PROXY_ADDR="127.0.0.1:${PROXY_PORT}"
PROXY_SERVICE_PORT="$( pick_free_port )"
PROXY_CA_PATH="${STATE_DIR}/host-daemon-proxy-ca.pem"

echo "host-daemon-control-plane-demo: step 3 — booting ${RIMSKY_PROXY_BIN} on ${PROXY_ADDR}"

RIMSKY_PROXY_GRPC_PORT="${PROXY_PORT}" \
RIMSKY_PROXY_SERVICE_GRPC_PORT="${PROXY_SERVICE_PORT}" \
RIMSKY_PROXY_LOCAL_CA_FILE="${PROXY_CA_PATH}" \
RIMSKY_CONTROL_API_URL="${CONTROL_URL}" \
RIMSKY_LOG_LEVEL=warn \
"${RIMSKY_PROXY_BIN}" >/dev/null 2>&1 &
PROXY_PID=$!

if ! poll_until_dialable_or_process_exits "${PROXY_ADDR}" "${PROXY_PID}"; then
    echo "host-daemon-control-plane-demo: FAIL — proxy exited before ${PROXY_ADDR} answered" >&2
    exit 1
fi

if [ ! -s "${PROXY_CA_PATH}" ]; then
    echo "host-daemon-control-plane-demo: FAIL — proxy did not publish its daemon-facing CA root at ${PROXY_CA_PATH}" >&2
    exit 1
fi

echo "host-daemon-control-plane-demo: step 4 — daemon start --proxy ${PROXY_ADDR} (anonymous mode, no api-key)"

START_STDOUT="$( "${RIMSKY_BIN}" daemon start \
    --proxy "${PROXY_ADDR}" \
    --tls-ca "${PROXY_CA_PATH}" \
    --state-dir "${STATE_DIR}" )"
echo "${START_STDOUT}"

case "${START_STDOUT}" in
    *"connected to ${PROXY_ADDR}"*) ;;
    *) echo "host-daemon-control-plane-demo: FAIL — start did not report 'connected to ${PROXY_ADDR}'" >&2
       exit 1 ;;
esac

DAEMON_PID="$( cat "${STATE_DIR}/daemon.pid" )"
echo "host-daemon-control-plane-demo: daemon pid ${DAEMON_PID}"

echo "host-daemon-control-plane-demo: step 5 — daemon status (expect connected)"

STATUS_STDOUT="$( "${RIMSKY_BIN}" daemon status --state-dir "${STATE_DIR}" )"
echo "${STATUS_STDOUT}"

case "${STATUS_STDOUT}" in
    *"connected"*) ;;
    *) echo "host-daemon-control-plane-demo: FAIL — status did not report 'connected'" >&2
       exit 1 ;;
esac

echo "host-daemon-control-plane-demo: step 6 — daemon stop (expect clean exit, no zombies)"

STOP_STDOUT="$( "${RIMSKY_BIN}" daemon stop --state-dir "${STATE_DIR}" )"
echo "${STOP_STDOUT}"

case "${STOP_STDOUT}" in
    *"stopped (pid ${DAEMON_PID})"*) ;;
    *) echo "host-daemon-control-plane-demo: FAIL — stop did not report the daemon stopped" >&2
       exit 1 ;;
esac

while kill -0 "${DAEMON_PID}" 2>/dev/null; do
    sleep 0.1
done
if [ -f "${STATE_DIR}/daemon.pid" ]; then
    echo "host-daemon-control-plane-demo: FAIL — stop did not remove ${STATE_DIR}/daemon.pid" >&2
    exit 1
fi

POST_STOP_STATUS="$( "${RIMSKY_BIN}" daemon status --state-dir "${STATE_DIR}" )"
case "${POST_STOP_STATUS}" in
    *"not running"*) ;;
    *) echo "host-daemon-control-plane-demo: FAIL — unexpected post-stop status, want 'not running'" >&2
       echo "${POST_STOP_STATUS}" >&2
       exit 1 ;;
esac

echo "host-daemon-control-plane-demo: all steps OK — start/status/stop lifecycle is sound"
