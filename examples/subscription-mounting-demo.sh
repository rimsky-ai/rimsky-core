#!/usr/bin/env bash
# Copyright © 2026 Fall Guy Consulting.
# Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
# repo root, or http://www.apache.org/licenses/LICENSE-2.0.

# subscription-mounting-demo.sh — STORY-subscription-mounting proof.
#
# An operator deploying an instance whose template declares publishers
# observes each publisher subscription progress from `mounting` to
# `active` on the instance surface — instead of trusting a create
# response that can silently mean "failed."
#
# The demo drives the story's Acceptance against a REAL assembled stack:
#
#   1. Boots `rimsky-all-in-one:latest` plus the bundled
#      `rimsky-sensor-object-store:latest` as the publisher peer, then
#      PAUSES the sensor container — the "publisher deliberately slow to
#      respond" condition (its gRPC server is frozen; Subscribe RPCs
#      black-hole until it wakes).
#   2. Registers a template with a `publishers:` block and creates an
#      instance: the create returns 201 IMMEDIATELY (no inline
#      Subscribe handshake), elapsed time printed.
#   3. GET /v1/instances/{id} shows the subscription visibly in state
#      `mounting` — the operator can SEE the publisher has not
#      acknowledged yet.
#   4. Unpauses the sensor; the reconciler retries Subscribe with no
#      attempt cap and the same GET flips to `active` WITHOUT any
#      operator action.
#   5. Drops an object into the sensor's bucket and tails the
#      instance's messages until the sensor's emit arrives — proof the
#      mounted subscription actually feeds the instance.
#
# Prerequisites the operator must satisfy BEFORE running this script:
#
#   1. Docker running, with the locally-built images present:
#      `make core-images service-images` produces
#      `rimsky-all-in-one:latest` and `rimsky-sensor-object-store:latest`.
#   2. `curl` and `python3` on $PATH (python3 parses the JSON
#      responses; no jq dependency).
#
# Output discipline: exits 0 only when every stage exhibited what the
# story promises — immediate 201, observable `mounting`, unattended
# flip to `active`, and a persisted publisher message. Anything else
# exits non-zero with a diagnostic.

set -euo pipefail

ALL_IN_ONE_IMAGE="${ALL_IN_ONE_IMAGE:-rimsky-all-in-one:latest}"
SENSOR_IMAGE="${SENSOR_IMAGE:-rimsky-sensor-object-store:latest}"

for bin in docker curl python3; do
    if ! command -v "${bin}" >/dev/null 2>&1; then
        echo "subscription-mounting-demo: missing required binary: ${bin}" >&2
        exit 1
    fi
done
for image in "${ALL_IN_ONE_IMAGE}" "${SENSOR_IMAGE}"; do
    if ! docker image inspect "${image}" >/dev/null 2>&1; then
        echo "subscription-mounting-demo: image ${image} not found locally —" >&2
        echo "  run 'make core-images service-images' first" >&2
        exit 1
    fi
done

# Unique names so concurrent runs (CI, repeated manual invocations)
# never collide on container/network names.
RUN_ID="$( date +%s )-$$"
NET="rimsky-sub-mount-demo-${RUN_ID}"
SENSOR="rimsky-sub-mount-demo-sensor-${RUN_ID}"
RIMSKY="rimsky-sub-mount-demo-rimsky-${RUN_ID}"
TMP_DIR="$( mktemp -d -t rimsky-sub-mount-demo.XXXXXXXX )"

# Cleanup: best-effort tear-down so a mid-script failure doesn't leave
# stray containers or the network dangling. Fires on EXIT regardless of
# exit code.
cleanup() {
    local rc=$?
    # Best-effort unpause first: a failure between `docker pause` and
    # `docker unpause` leaves the sensor paused, and older daemons
    # refuse `rm -f` on a paused container — which would then strand
    # the network too (both errors swallowed by the `|| true`s below).
    docker unpause "${SENSOR}" >/dev/null 2>&1 || true
    docker rm -f "${SENSOR}" "${RIMSKY}" >/dev/null 2>&1 || true
    docker network rm "${NET}" >/dev/null 2>&1 || true
    rm -rf "${TMP_DIR}"
    exit "${rc}"
}
trap cleanup EXIT

# json_get <python-expr> reads JSON on stdin and prints the evaluated
# expression against the parsed object bound as `d`. Empty output on
# any parse/lookup error so callers can poll without aborting mid-loop.
json_get() {
    python3 -c "
import sys, json
try:
    d = json.load(sys.stdin)
    v = (${1})
    print(v if v is not None else '')
except Exception:
    pass
" 2>/dev/null
}

echo "subscription-mounting-demo: [1/6] booting the stack (network ${NET})"
docker network create "${NET}" >/dev/null

# The sensor peer comes up FIRST so rimsky's startup eager-dial of the
# declared publisher succeeds; the slowness is injected by PAUSING the
# container afterwards, not by making it unreachable at boot.
docker run -d --name "${SENSOR}" \
    --network "${NET}" --network-alias sensor \
    -e RIMSKY_SENSOR_OBJECT_STORE_PORT=9083 \
    -e RIMSKY_ENDPOINT=http://rimsky:8080 \
    -e RIMSKY_SENSOR_OBJECT_STORE_FS_ROOT=/data/object-store \
    "${SENSOR_IMAGE}" >/dev/null

# rimsky.yml: the all-in-one SQLite default plus the publishers: block
# naming the sensor peer, and ref_validation_mode none so the demo
# template's executor-less reactor node registers (the proof axis is the
# subscription lifecycle + the persisted publisher message, not the
# reactor's run).
cat > "${TMP_DIR}/rimsky.yml" <<'YAML'
persistence:
  driver: sqlite
  sqlite:
    path: /var/lib/rimsky/state.db
templates:
  ref_validation_mode: none
claim_producers: {}
named_locks: {}
executors: {}
publishers:
  watcher:
    endpoint: "sensor:9083"
    protocols: [publisher]
YAML

docker run -d --name "${RIMSKY}" \
    --network "${NET}" --network-alias rimsky \
    -p 127.0.0.1:0:8080 \
    -v "${TMP_DIR}/rimsky.yml:/etc/rimsky/rimsky.yml:ro" \
    "${ALL_IN_ONE_IMAGE}" >/dev/null

PORT="$( docker port "${RIMSKY}" 8080 | head -n1 | sed 's/.*://' )"
BASE="http://127.0.0.1:${PORT}"

for _ in $( seq 1 120 ); do
    if curl -fsS "${BASE}/v1/health" >/dev/null 2>&1; then break; fi
    sleep 0.5
done
if ! curl -fsS "${BASE}/v1/health" >/dev/null 2>&1; then
    echo "subscription-mounting-demo: rimsky never became healthy at ${BASE}" >&2
    docker logs "${RIMSKY}" >&2 || true
    exit 1
fi
echo "subscription-mounting-demo: stack healthy at ${BASE}"

echo "subscription-mounting-demo: [2/6] pausing the sensor — the publisher is now deliberately slow to respond"
docker pause "${SENSOR}" >/dev/null

# Register + deploy the template with a publishers: block. The watcher
# publisher polls the filesystem-backend bucket `events` every second.
TEMPLATE_BODY='{
  "spec": {
    "name": "subscription-mounting-demo",
    "version": "1",
    "frame_resolution_mode": "serial_queue",
    "frame_timeout_ms": 600000,
    "nodes": [
      {
        "type": "reactor",
        "subscribes": [
          {"instance": true, "type": "message/invalidate/publisher/reactor", "frame": "in"}
        ]
      }
    ],
    "publishers": [
      {
        "name": "watcher",
        "kind": "object-store",
        "config": {"backend": "filesystem", "bucket": "events", "prefix": "", "poll_interval": "1s", "watermark_field": "name"},
        "target_node": "reactor",
        "message_kind": "invalidate"
      }
    ]
  }
}'
TEMPLATE_ID="$( curl -fsS -X POST -H 'Content-Type: application/json' \
    -d "${TEMPLATE_BODY}" "${BASE}/v1/templates" | json_get "d['template_id']" )"
if [ -z "${TEMPLATE_ID}" ]; then
    echo "subscription-mounting-demo: template registration returned no template_id" >&2
    exit 1
fi
curl -fsS -X POST -H 'Content-Type: application/json' -d '{}' \
    "${BASE}/v1/templates/${TEMPLATE_ID}/deploy" >/dev/null
echo "subscription-mounting-demo: template ${TEMPLATE_ID} registered + deployed"

echo "subscription-mounting-demo: [3/6] creating the instance — the create must return 201 immediately, paused publisher notwithstanding"
CREATE_START="$( python3 -c 'import time; print(time.time())' )"
CREATE_OUT="$( curl -sS -X POST -H 'Content-Type: application/json' \
    -d "{\"template\": \"${TEMPLATE_ID}\", \"instance_key\": \"sub-mount-demo-${RUN_ID}\", \"params\": {}}" \
    -w '\n%{http_code}' "${BASE}/v1/instances" )"
CREATE_END="$( python3 -c 'import time; print(time.time())' )"
CREATE_CODE="$( printf '%s' "${CREATE_OUT}" | tail -n1 )"
CREATE_JSON="$( printf '%s' "${CREATE_OUT}" | sed '$d' )"
ELAPSED="$( python3 -c "print(f'{${CREATE_END} - ${CREATE_START}:.3f}')" )"
if [ "${CREATE_CODE}" != "201" ]; then
    echo "subscription-mounting-demo: POST /v1/instances returned ${CREATE_CODE}, want 201: ${CREATE_JSON}" >&2
    exit 1
fi
INSTANCE_ID="$( printf '%s' "${CREATE_JSON}" | json_get "d['instance_id']" )"
if [ -z "${INSTANCE_ID}" ]; then
    echo "subscription-mounting-demo: create response carried no instance_id: ${CREATE_JSON}" >&2
    exit 1
fi
echo "subscription-mounting-demo: 201 Created in ${ELAPSED}s — instance_id=${INSTANCE_ID}"
# "Immediately" must be falsifiable: the old inline-Subscribe path
# would burn its multi-second retry budget against the paused sensor
# before responding. 2s is generous slack for HTTP + SQLite writes
# while still refuting any inline publisher handshake.
python3 -c "import sys; sys.exit(0 if ${ELAPSED} < 2.0 else 1)" || {
    echo "subscription-mounting-demo: create took ${ELAPSED}s — not the immediate return the story promises" >&2
    exit 1
}

echo "subscription-mounting-demo: [4/6] inspecting the instance — the subscription must be visibly 'mounting'"
SUB_STATE="$( curl -fsS "${BASE}/v1/instances/${INSTANCE_ID}" \
    | json_get "', '.join(s['publisher_name'] + '=' + s['state'] for s in d.get('subscriptions') or [])" )"
echo "subscription-mounting-demo: GET /v1/instances/${INSTANCE_ID} → subscriptions: ${SUB_STATE}"
case "${SUB_STATE}" in
    *"watcher=mounting"*) ;;
    *)
        echo "subscription-mounting-demo: expected watcher=mounting while the publisher is paused, got: ${SUB_STATE}" >&2
        exit 1
        ;;
esac

echo "subscription-mounting-demo: [5/6] unpausing the sensor — the subscription must flip to 'active' with NO operator action"
docker unpause "${SENSOR}" >/dev/null
ACTIVE=""
for _ in $( seq 1 120 ); do
    # `|| true`: a transient curl/parse failure inside a poll loop must
    # not abort the demo under `set -euo pipefail` — the loop retries.
    SUB_STATE="$( curl -fsS "${BASE}/v1/instances/${INSTANCE_ID}" \
        | json_get "', '.join(s['publisher_name'] + '=' + s['state'] for s in d.get('subscriptions') or [])" || true )"
    echo "subscription-mounting-demo:   subscriptions: ${SUB_STATE}"
    case "${SUB_STATE}" in
        *"watcher=active"*) ACTIVE=1; break ;;
        *"watcher=failed"*)
            echo "subscription-mounting-demo: subscription flipped to failed — a paused-then-woken publisher is the retryable class" >&2
            exit 1
            ;;
    esac
    sleep 1
done
if [ -z "${ACTIVE}" ]; then
    echo "subscription-mounting-demo: subscription never reconciled to active within 120s of the publisher waking" >&2
    docker logs "${RIMSKY}" >&2 || true
    exit 1
fi
echo "subscription-mounting-demo: subscription is active — the reconciler converged unattended"

echo "subscription-mounting-demo: [6/6] dropping an object into the sensor's bucket — the sensor's message must arrive on the instance"
# Stage the whole bucket tree locally and copy it from the root: the
# distroless sensor image has no shell and no pre-baked /data, and
# `docker cp` (unlike the test harness's tar-based CopyToContainer)
# does not create missing intermediate directories at the destination.
# Copying the staged `data/` directory into `/` creates the full
# /data/object-store/events/ tree in one shot. World-readable modes so
# the nonroot sensor process can traverse + read what the daemon writes
# as root.
mkdir -p "${TMP_DIR}/stage/data/object-store/events"
printf '{"event":"created","payload":"demo"}' > "${TMP_DIR}/stage/data/object-store/events/001-event.json"
chmod -R a+rX "${TMP_DIR}/stage/data"
docker cp "${TMP_DIR}/stage/data" "${SENSOR}:/" >/dev/null

MESSAGE=""
for _ in $( seq 1 60 ); do
    # `|| true`: transient curl/parse failures retry on the next poll.
    MESSAGE="$( curl -fsS "${BASE}/v1/instances/${INSTANCE_ID}/messages?sender_kind=publisher" \
        | json_get "json.dumps((d.get('messages') or [None])[0])" || true )"
    if [ -n "${MESSAGE}" ] && [ "${MESSAGE}" != "null" ]; then break; fi
    MESSAGE=""
    sleep 1
done
if [ -z "${MESSAGE}" ]; then
    echo "subscription-mounting-demo: no publisher message arrived within 60s of the object drop" >&2
    docker logs "${SENSOR}" >&2 || true
    exit 1
fi
echo "subscription-mounting-demo: publisher message persisted on the instance:"
echo "  ${MESSAGE}"

echo "subscription-mounting-demo: complete — immediate 201, observable mounting, unattended flip to active, sensor messages flowing"
