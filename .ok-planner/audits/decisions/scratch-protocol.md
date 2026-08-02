---
audit: scratch-protocol
artifact: decision:scratch-protocol
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:28:34Z
---

# Scratch rides ExecuteRequest and the three settling terminal outcomes only

Supported. `executor.proto` carries `scratch` on `ExecuteRequest` and on all three settling-outcome messages of the `Outcome` oneof — `Success`, `Error`, `Park` — while `AwaitAsyncCallback` (the transient hand-off variant) carries no scratch field, and `AsyncCallbackBody` mirrors the same three-variant oneof for the callback leg; checked all four `Outcome` variants plus the callback body message against the proto source. The Go terminal-handling code (`applyTerminalScratchInTx` in `runner_terminal_scratch.go`) is invoked from all three settling-outcome handlers — success (`runner_terminal.go`), park (`runner_terminal_park.go`), and error/infra (`runner_error_policy.go`) — and from the async-callback dispatcher (`callback.go`), and its first line makes a zero-length scratch payload a no-op against the persisted row rather than a clear, so prior scratch survives an empty terminal-attach. A unit test (`TestApplyTerminalScratchInTx_EmptyScratchIsNoOp`) exercises the nil- and empty-byte-slice no-op cases directly, and the same-named end-to-end scenario test round-trips non-empty scratch through real dispatches.
