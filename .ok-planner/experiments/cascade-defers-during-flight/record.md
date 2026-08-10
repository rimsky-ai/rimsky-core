---
experiment: cascade-defers-during-flight
commit: PENDING
---

# In-flight node-runs sealed against cascade

The story makes two claims, so this directory holds two runnable ways.

## way-inflight-seal.py

### What it runs against

The script boots a `rimsky-all-in-one` container at `RIMSKY_IMAGE_TAG` (or reuses
the control API named by `RIMSKY_CONTROL_API_URL`) and drives it through the
control API.

The template declares a counter node and a worker node that subscribes to it and
reads its count. The script installs a pause-mode pre-dispatch breakpoint on the
worker, so the worker's first run sits in flight. While it sits there, the script
invalidates the counter through the debug-override channel, which makes the
counter run a second time and cascade to the worker.

### What was observed

The breakpoint hit snapshot showed the worker's dispatch bag holding the count of
its own moment, 1. The cascade during that run left the running node-run row
untouched and added a second node-run row in `pending`. After the hit was
resumed, the first run settled carrying 1, not the freshened 2. The second run
dispatched only after the first settled, and its own breakpoint hit showed 2.

Seven checks, none failing.

## way-parked-wake.py

### What it runs against

The script creates a docker network, starts `rate-limited-endpoint.py` on it in a
`python:3.12-alpine` container, and boots a `rimsky-all-in-one` container on the
same network with `RIMSKY_EXECUTOR_HTTP_NODE_EGRESS_ALLOWLIST` set to the
network's subnet. The endpoint answers the first request with HTTP 429 and
`Retry-After: 3600`, and every later request with HTTP 200.

The template declares a counter node and an `http-node` worker that subscribes to
it and calls the endpoint. The first dispatch parks. The script then pauses the
instance, invalidates the counter through the debug-override channel, and
resumes.

### What was observed

The worker parked with a resume time one hour out. The upstream cascade woke it
within a second, recording `resume_reason: upstream_cascade`. The woken work then
reached the endpoint and settled successfully.

Five checks, none failing.
