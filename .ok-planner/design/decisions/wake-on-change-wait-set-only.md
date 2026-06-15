---
decision: wake-on-change-wait-set-only
status: as-is
---

# wake_on_change false inserts the wait-set row but skips the stale-mark

## Choice

At cascade walk time, a matching subscription edge inserts a wait-set row for the receiver on the sender's run when the receiver has an in-flight run in the sender's RunScope. The receiver is additionally stale-marked iff the edge's `wake_on_change` is `true`.

When the edge's `wake_on_change` is `false` and the receiver has no in-flight run at the time of the sender's settle (the sender settled before any other edge pulled the receiver into the frame), the wait-set row is skipped — there is no receiver row to gate. A later wake of the receiver through another subscription resolves the substitution ref via the fallback / lenient / optional routing (see `decision:substitution-grammar-fallback-routing`). Authors who require deterministic carry-through regardless of intra-frame ordering use `force_upstream_refresh: true` on the receiving subscription instead.

## Rationale

Decouples context-gathering reads from cascade dispatch. The receiver's wake-up is governed by its other subscriptions; its substitution context receives the sender's data when the receiver is already in the frame at the sender's settle. The wait-set row's receiver-keyed shape gates the design to that ordering — the alternative (lazy-allocating a non-dispatching receiver row solely to anchor a wait-set entry) widens the persistence contract; the fallback routing already covers the ordering-gap case.
