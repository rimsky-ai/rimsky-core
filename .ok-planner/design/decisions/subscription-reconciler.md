---
decision: subscription-reconciler
---

# A reconciliation worker drives publisher Subscribe

## Choice

A reconciliation worker performs Subscribe RPCs for mounting subscription rows at a fixed reconcile interval with no attempt cap; the failed state is reserved for non-retryable errors (e.g. an unknown publisher name); the startup resync pass remains the durable safety net.

## Rationale

Retry-forever matches desired-state semantics; bounded retry budgets convert contention spikes into silent failures.

## Alternatives

- A bounded retry budget that lands exhausted rows in failed — rejected: converts contention spikes into silent failures; failed then stops meaning "non-retryable".
- No worker, relying on the startup resync pass alone — rejected: a mounting row would wait for a process restart to mount.
