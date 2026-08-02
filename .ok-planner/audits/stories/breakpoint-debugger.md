---
audit: breakpoint-debugger
artifact: story:breakpoint-debugger
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:41:29Z
---

# Operator breakpoint-debugger lifecycle: install, dual-surface hit visibility, overlay resume, cascade-clear delete

Supported. `test/scenarios/breakpoints/debugger_lifecycle_e2e_test.go::TestBreakpointDebuggerLifecycleE2E` (tagged `@story: breakpoint-debugger`) walks the full lifecycle against a real stack: it installs a `before_dispatch` breakpoint via `POST /v1/instances/{id}/breakpoints`, confirms the resulting hit appears both on the unified `/v1/events?kind=breakpoint.hit` feed and on the dedicated `/v1/instances/{id}/breakpoint-hits` ledger (asserting the two counts stay equal and co-transactional), resumes the hit with an attribute overlay via `POST /v1/instances/{id}/breakpoints/{bp}/resume` and confirms the executor's next dispatch and the node's `latest_attributes` both carry the overlay value, then deletes the breakpoint and confirms it is gone from the breakpoints list and its hit is cascade-deleted from the ledger. Supporting unit coverage (`hit_emits_event_test.go`, `resume_with_overlay_test.go`, `orphan_hit_on_breakpoint_deletion_test.go`) exercises the same four legs individually.
