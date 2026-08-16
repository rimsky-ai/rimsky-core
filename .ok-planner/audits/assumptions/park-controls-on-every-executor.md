---
assumption: park-controls-on-every-executor
commit: d977250c
disposition: trap
synthesized: 2026-08-16T05:48:16Z
---

# the park controls are a platform-level attribute family, so `probe_park`, `probe_cancel`, and `park_resume_at` work on any executor, not just `http-node`.

As template author testing park-and-resume, I would take it that the park controls are a platform-level attribute family, so `probe_park`, `probe_cancel`, and `park_resume_at` work on any executor, not just `http-node`.

## Source

sibling-symmetry — three park/cancel keys under `http-node` only, against `concept:parked-state` as a runtime-wide node state

## What a run would observe

declare `park_resume_at` on a `claude-agent` node and see whether the attribute is accepted at registration.

## Measured

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
