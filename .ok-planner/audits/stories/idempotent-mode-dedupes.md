---
audit: idempotent-mode-dedupes
artifact: story:idempotent-mode-dedupes
determination: supported
commit: b767a27d
audited: 2026-08-02T09:41:29Z
---

# Idempotent cascade modes drop byte-identical re-runs before dispatch

Supported. The gate evaluator (`lib/runtime/gate_evaluator.go`) implements both `cascade.CascadeModeIdempotentQueue` and `cascade.CascadeModeIdempotentSettled` through a shared `modeDropIfPriorEqual` helper: it JCS-compares the newly-resolved bag against the prior cascade row for the `(receiver, run-scope)`, and for the settled variant additionally falls back to `GetMostRecentSettledRun` when no queued prior exists, dropping (deleting) the transitioning row on a byte-equivalent match instead of promoting it to `stale`. `test/scenarios/idempotent_mode_dedupes_test.go` (tagged `@story: idempotent-mode-dedupes`) covers both of the story's two named comparison scopes end-to-end against a real stack: `TestIdempotentModeDedupes_QueueComparison` drives five cascade rounds through an `idempotent-queue` node and confirms the downstream executor is invoked exactly once (four re-runs deduped at pending→stale); `TestIdempotentModeDedupes_SettledComparison` drives a settle boundary — a first cascade round dispatches and settles, then an operator-invalidate retrigger of the upstream produces a bag-identical second cascade round, and confirms the `idempotent-settled` node is still invoked exactly once and shows a single run row (the dropped pending is deleted, not left behind).
