---
audit: entry-absorption-flag
artifact: decision:entry-absorption-flag
determination: supported
commit: b767a27d
audited: 2026-08-02T09:33:37Z
---

# Entry absorption is an input flag on DispatchChildren, not a pre-step

Supported. `lib/runtime/child_execution.go`'s `ChildExecutionInput` carries an `EntryAbsorbed bool` field, and `DispatchChildren` — the sole dispatch-children primitive, confirmed by its two call sites (`lib/runtime/subgraph_dispatch.go` passing `EntryAbsorbed: true` for delegation, `lib/runtime/fanout_dispatch.go` passing `EntryAbsorbed: false` for fan-out) — branches on that field inline (`if in.EntryAbsorbed { rejectDelegateRecursionInChain(...) }`) rather than through a separate pre-dispatch step. No standalone "absorb entry" runtime function exists outside `DispatchChildren` (the only other `absorb`-named function, `absorbEntryIntoCaller` in `lib/graph/node/template_validator_graphs.go`, is a distinct template-canonicalization step at registration time, not a runtime dispatch pre-step). `lib/runtime/child_execution_test.go` and `lib/runtime/auto_terminal_test.go` exercise the recursion-rejection behavior gated by this flag.
