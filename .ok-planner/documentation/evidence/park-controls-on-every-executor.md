---
trap: park-controls-on-every-executor
release: d977250c
---
# Evidence set — the park controls are a platform-level attribute family, so `probe_park`, `probe_cancel`, and `park_resume_at` work on any executor, not just `http-node`.

Source of the prior: sibling-symmetry — three park/cancel keys under `http-node` only, against `concept:parked-state` as a runtime-wide node state

## What the audit ran and observed (assumption record)

The experiment `assumption-park-controls-on-every-executor` declared
`probe_park: true` and `park_resume_at: 2031-03-04T05:06:07Z` on one node per
bundled executor, all four under `RIMSKY_EXECUTOR_STUB_MODE=1`. Every template
registered — the attributes are accepted everywhere — and two of the four
executors park. `http-node` and `claude-agent` each emitted `transient/park`
and appear in `GET /v1/admin/diagnostics/parked-nodes` with exactly the
declared resume time. `verifier-http` completed normally with nothing parked,
and `verifier-shape-checks` errored with `verifier/attribute_invalid` on its
real check path. Parked is indeed a runtime-wide node state, but the controls
that reach it are per-executor stub-mode probes, not a platform attribute
family: outside stub mode they do nothing at all, as the ordinary http-node
showed by making the real request and failing with `http/network_error`. A
template author testing park-and-resume on a verifier node gets silence or an
unrelated error, and the attributes register cleanly either way.

## Experiment record (experiment:assumption-park-controls-on-every-executor)

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

Runnables: `src:.ok-planner/experiments/assumption-park-controls-on-every-executor/` at the stamped commit.
