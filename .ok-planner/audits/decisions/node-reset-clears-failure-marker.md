---
audit: node-reset-clears-failure-marker
artifact: decision:node-reset-clears-failure-marker
determination: supported
commit: b767a27d
audited: 2026-08-02T09:29:05Z
---

# Node-reset clears the failed run's settling-signal marker and takes no dispatch-affecting action

Supported. `handleResetNode` (`lib/control/controlapi/nodes.go`) returns 409 Conflict unless the node has a failed-terminal run in some scope (the state gate), then calls `Nodes().ResetFailedTerminalSettlingSignalType` to null out the failed row's `settling_signal_type` and appends an `operator_override` audit event — no message is posted, no frame is opened, and no claim/dispatch-eligibility field is touched anywhere in the handler. `TestResetFailedNodeDrivesThroughFrameEngine` (`test/scenarios/frame_resolution/reset_failed_node_drives_through_frame_engine_test.go`) confirms the failed row's `settling_signal_type` reads NULL after reset and directly demonstrates the two-step operator workflow the decision describes: reset alone leaves the node inert, and only a subsequent invalidating message drives a fresh dispatch to `fresh`. `node_admin_e2e_test.go` independently confirms the 409-on-non-failed-node state gate. The per-dispatch retry-budget claim is corroborated by `concept:node-run`'s documented `retry_counter` field, which is initialized to zero at row creation and never carried across rows, matching "every new run starts with a fresh budget."
