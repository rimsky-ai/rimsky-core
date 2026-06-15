---
decision: subscription-reconciler
status: as-is
---

# A reconciliation worker drives publisher Subscribe

## Choice

A reconciliation worker performs Subscribe RPCs for mounting subscription rows with backoff and no attempt cap; the failed state is reserved for non-retryable errors (e.g. an unknown publisher name); the startup resync pass remains the durable safety net.

## Rationale

Retry-forever matches desired-state semantics; bounded retry budgets convert contention spikes into silent failures.
