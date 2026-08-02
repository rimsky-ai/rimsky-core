---
audit: exit-codes
artifact: decision:exit-codes
determination: unsupported
commit: b767a27d
audited: 2026-08-02T09:58:10Z
issue: 2026-08-02-095810-exit-codes-park-timeout-clause-stale
---

# Exit-code classes

Unsupported for one clause and one cross-reference; the exit-code scheme itself is correctly implemented and directly tested across all four classes. The decision's parenthetical naming park-timeout as a covered failure case is stale: the park-timeout mechanism it refers to was fully retired by a numbered migration, and the state machine now rejects that transition reason as illegal, so no run can reach a failed terminal by that route anymore. Separately, the decision's own pointer to a named concept document for run-timeout and park-timeout semantics is broken — that document's text mentions neither term.
