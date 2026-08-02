---
audit: no-resume-context
artifact: decision:no-resume-context
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:32:02Z
---

# No dedicated resume-context channel; scratch carries park-crossing executor state on the same dispatch row

Supported. The wire proto reserves both the retired `ExecuteRequest.resume_context` field (13) and `userdata` (4) — enforced mechanically by `TestExecuteRequest_ReservesUserdataAndResumeContext` over the live proto descriptor — leaving no resume-payload or session-token field on the dispatch request (a repo-wide search finds no `session_token`/`SessionToken` field anywhere in the protocol). `applyTerminalPark` (`lib/runtime/runner_terminal_park.go`, annotated `@decision: no-resume-context`) persists the Park outcome's `scratch` bytes via `applyTerminalScratchInTx` and updates the same node-run row to the `parked` state through `ParkActive`, which is implemented as an `UPDATE rimsky_node_runs ... WHERE id = ...` (verified in `lib/foundation/persistence/sqlite/queue_park.go`) rather than a row-copy — the parked row is the resume row. Park's terminal-event type carries no `attributes_delta` field, consistent with the claim that attribute writeback is unavailable on Park.
