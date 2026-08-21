---
decision: subscription-reconciler
---

# A reconciliation worker drives publisher Subscribe

## Choice

A reconciliation worker performs Subscribe RPCs for mounting subscription rows at a fixed reconcile interval with no attempt cap; the failed state is reserved for non-retryable errors (e.g. an unknown publisher name); the startup resync pass remains the durable safety net — it lists each publisher's live subscriptions and issues only the rows missing from that set, never re-issuing an already-active subscription. The resync pass also recovers one class of failed row: a row that failed on an unregistered publisher name flips back to mounting once that name registers. Every other failed class stays failed.

## Rationale

Retry-forever matches desired-state semantics; bounded retry budgets convert contention spikes into silent failures.

An unregistered publisher name is non-retryable only while the name is absent, and registering the publisher clears that condition. Recovering that one class at resync keeps a deploy-order mistake from costing an instance its subscription. Every other class stays failed, so failed keeps meaning non-retryable.

## Alternatives

- A bounded retry budget that lands exhausted rows in failed — rejected: converts contention spikes into silent failures; failed then stops meaning "non-retryable".
- No worker, relying on the startup resync pass alone — rejected: a mounting row would wait for a process restart to mount.
- Leave every failed row failed until an operator recreates the instance — rejected: registering a publisher a moment late would cost that instance its subscription for a condition that has since cleared.
