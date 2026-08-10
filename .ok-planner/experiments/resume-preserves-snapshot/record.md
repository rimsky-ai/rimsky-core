---
experiment: resume-preserves-snapshot
commit: PENDING
---

# What a resumed park is dispatched with

The story promises the resumed executor sees the values it parked with even when
upstream nodes re-ran during the park, so this directory holds two runnable ways:
one where nothing upstream moves, one where it does.

Both ways use `rate-limited-endpoint.py`, which answers the first request with
HTTP 429 and a `Retry-After` header, answers every later request with HTTP 200,
and records every request body it received. Reading that log is how each way sees
what the executor was actually dispatched with.

## way-timer-resume.py

### What it ran against

A docker network carrying the endpoint container with `RETRY_AFTER=2` and a
`rimsky-all-in-one` container with `RIMSKY_EXECUTOR_HTTP_NODE_EGRESS_ALLOWLIST`
set to the network's subnet. The template declares a counter node and an
`http-node` worker that subscribes to it and sends the counter's value to the
endpoint. Nothing invalidates the counter, so the park resumes on its own retry
schedule.

### What was observed

The worker's first dispatch sent `{"seen": 1}` and parked. The park resumed with
`resume_reason: deadline_elapsed`, and the endpoint received a second request
with the same body, `{"seen": 1}`. The run that executed after the wake carried
the same run id as the parked run, and the worker settled once across the two
dispatches.

Five checks, none failing.

## way-upstream-moves-during-park.py

### What it ran against

The same shape, with the endpoint's `Retry-After` left at 3600 so the park cannot
resume on its own. Once the worker is parked, the script pauses the instance,
invalidates the counter node through the debug-override channel, and resumes the
instance — the public path by which an upstream node re-runs while work sits
parked.

### What was observed

The worker's first dispatch sent `{"seen": 1}` and parked with a resume time an
hour out. The counter node re-ran and settled at 2. The parked work was woken,
recording `resume_reason: upstream_cascade`.

The work that then ran was not the parked run. It carried a different run id, and
the endpoint received `{"seen": 2}` — the freshened upstream value, not the value
the run parked with. The parked run never completed; the only worker run that
completed was the new one. The endpoint received exactly two requests across the
whole episode, so the parked unit of work was never re-executed at all.

The same three checks failed on two independent runs of the script.

Five checks pass, three fail.

RESULT: FAIL — on the only public path where an upstream node re-runs during a
park, the parked run is replaced by a new run whose inputs were re-substituted
from the freshened upstream.
