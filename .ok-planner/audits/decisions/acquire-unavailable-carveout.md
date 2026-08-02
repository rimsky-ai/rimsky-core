---
audit: acquire-unavailable-carveout
artifact: decision:acquire-unavailable-carveout
determination: supported
commit: b767a27d
audited: 2026-08-02T09:35:26Z
---

# acquire-unavailable and acquire-producer-error handlers sit outside the unified claim-handle resolution engine

Supported. `lib/runtime/runner_acquire_error_policy.go::handleAcquireUnavailable` and `::handleAcquireProducerError` (the latter's shared downstream call carries the decision's own citation tag on `runAcquireErrorPolicy`) both call `abandonPartialLocks` (an outbox-enqueued Abandon verb, no claim-handle row delete) and then the shared `runAcquireErrorPolicy`, differing only in the reported error class (`acquire/unavailable` vs `acquire/producer_error`) and payload fields (`unavailable`/`producer` keys); neither calls `ResolveClaimHandleTerminal` (`lib/runtime/terminal_decision.go`), the unified verb-then-delete resolution engine used by every other terminal path in the package (auto-terminal, child settlement, cancellation, instance kill, terminal release). This matches the rationale that both handlers' acquisition transactions have already rolled back, so there is no committed row to fold into the engine's delete step.
