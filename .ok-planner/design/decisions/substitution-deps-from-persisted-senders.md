---
decision: substitution-deps-from-persisted-senders
status: as-is
---

# Substitution-context deps read from subscribed senders' persisted attribute stores

## Choice

The substitution-context builder enumerates the receiver's subscribed sender node types from the template's subscription-edge map (`concept:node-subscription`), and for each sender node queries its most-recent fresh-settled run's attribute data directly from the per-run attribute ledger (`concept:attribute`). The same builder serves the receiver's gate-eval `pending → stale` transition and any acquisition-time deps lookup (e.g., lock-name substitution).

The wait-set (`concept:wait-set`) is not consulted for sender attribute data; it carries only wake/eligibility metadata. Wait-set drain triggers gate-eval; gate-eval then queries the persisted store for sender values.

## Rationale

Splits the wait-set's role cleanly: wake-vs-data. Wait-set rows drive *when* a receiver is evaluated; the persisted attribute store is the source of truth for *what* each sender produced. The two are independent — a sender's diff-based attribute cascade (`concept:cascade`) may emit no `attribute/<key>/changed` signal for an unchanged value, but the receiver can still substitute against that sender's current attributes because the lookup is by node identity against the persisted store, not by signal payload presence. Conversely, a stale wait-set row from a prior cascade round doesn't deliver yesterday's snapshot; substitution always reads the current value.

## Alternatives

- **Read from drained wait-set rows.** The original model keyed substitution on row presence. Retired — tied substitution-availability to cascade-emission decisions, which under diff-based attribute cascade broke substitution for unchanged-but-needed sender values. See `decision:_retired/substitution-context-builder-reads-drained-rows`.
- **Carry sender snapshots on cascade signals.** Include the sender's full attribute snapshot in the cascade payload, propagate it through the wait-set, deliver to the receiver. Rejected: payload propagation through the wait-set is a deliberate non-feature (`concept:signal` — wait-set rows carry no payload), and snapshots would diverge from the persisted store as soon as the sender's next run settled. Reading from the store is both simpler and more current.
