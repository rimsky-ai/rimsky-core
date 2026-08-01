---
decision: node-reset-clears-failure-marker
status: as-is
---

# Node reset clears the failed run's settling-signal marker

## Choice

The node-reset endpoint is an observability-surface verb. It preserves the state gate (refusing non-failed-terminal nodes with a conflict rejection); on a valid reset it clears the failed run's persisted settling-signal marker so the node-inspect surface no longer reports a settled failure; it does not enqueue an envelope, open a frame, or affect dispatch eligibility in any way. Retry budgets are per-dispatch (`concept:node-run`): every new run starts with a fresh budget, so no cross-run budget exists for reset to act on. The operator's workflow for retrying an errored node remains two explicit steps: reset (clearing the stale marker), then a message that invalidates the node so a fresh dispatch is attempted.

## Rationale

The marker-clear is genuinely observable on the node-inspect surface and keeps a non-debug operator recourse for tidying a failed node's reported state. A framing that claims an effect on acquisition eligibility would be false — the per-dispatch budget always starts fresh, so no reset can change whether the next run dispatches.

## Alternatives

- Retire the endpoint — rejected: loses the only non-debug operator verb for clearing a stale failure marker; the observability value is modest but real.
- Keep the retry-budget framing — rejected: documents an effect the per-dispatch budget design makes impossible.
- Fold reset into the debug-channel surface — rejected: requires the operator to pause first, a heavier workflow for common-case tidy-up.
