---
audit: node-admin
artifact: story:node-admin
determination: supported
commit: b767a27d
audited: 2026-08-02T09:43:53Z
---

# Operator inspects a node's full detail and clears a failed-terminal node's stale failure marker

Supported. `GET /v1/nodes/{id}` (`handleGetNode`, `lib/control/controlapi/nodes.go`) returns the node's full detail — type, executor, tags, cascade mode, latest attributes, settling signal type, and the categorical per-state run summary — and 404s for an unknown id. `POST /v1/nodes/{id}/reset` (`handleResetNode`, tagged `@story: node-admin` and `@decision: node-reset-clears-failure-marker`) clears a failed-terminal node's stale failure marker: it looks up a failed terminal run scope and returns 409 when none exists (refusing reset on a non-failed node), otherwise clears the marker via `ResetFailedTerminalSettlingSignalType` and writes an `operator_override` audit event. `test/scenarios/node_admin_e2e_test.go::TestAcceptance_NodeAdmin_GetAndReset` (tagged `@story: node-admin`) checks both surfaces end-to-end against a real stack: GET on a fresh worker and a failed-terminal node both return the expected run-summary detail, GET on an unknown id 404s, reset on the still-fresh worker 409s, and reset on the failed node succeeds, after which a fresh wake drives the node through a real re-fire to a successful terminal (`fresh` state, incremented terminal-success count) — proving the stale failure marker no longer masks the node's true current state. `test/scenarios/frame_resolution/reset_failed_node_drives_through_frame_engine_test.go` (also tagged `@story: node-admin`) independently confirms the reset drives back through the frame engine.
