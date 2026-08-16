---
experiment: assumption-park-controls-on-every-executor
commit: PENDING
---

# Declaring probe_park and park_resume_at on a node per bundled executor

## What it ran against

One `rimsky-all-in-one` stack with all four bundled executors, each started
with `RIMSKY_EXECUTOR_STUB_MODE=1`, plus one `rimsky-executor-http-node` in its
ordinary mode. Each executor gets one node declaring `probe_park: true` and
`park_resume_at: 2031-03-04T05:06:07Z`. The run reads the instance events and
`GET /v1/admin/diagnostics/parked-nodes`.

## What was observed

All five templates registered — every executor's schema accepts the park
attributes — and two of the four executors park.

`http-node` and `claude-agent` each emitted `transient/park` and appear in the
parked-nodes diagnostic with `resume_at` exactly `2031-03-04T05:06:07Z`, the
time the template named.

`verifier-http` took the same attributes and completed normally: no park event,
nothing in the parked-nodes list. `verifier-shape-checks` took them and errored
with `verifier/attribute_invalid`, having run its real check path.

Outside stub mode the controls do nothing at all: the same node against the
ordinary http-node parked nothing and failed with `http/network_error`, the
real request the park probe was supposed to displace.
