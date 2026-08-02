---
decision: substitution-deps-from-persisted-senders
---

# Substitution-context deps read from subscribed senders' persisted attribute stores

## Choice

The substitution-context builder enumerates the receiver's subscribed sender node types from the template's subscription-edge map (`concept:node-subscription`), and for each sender queries its most-recent fresh-settled run's attribute data **in the current frame's RunScope tree** directly from the per-run attribute ledger (`concept:attribute`). Sender-run rows from any other frame are invisible to the query — RunScopes never span frames (per `concept:run-scope`), and per `concept:frame`'s isolation invariant, no substitution lookup may reach across a frame boundary. The same builder serves the receiver's gate-eval `pending → stale` transition and any acquisition-time deps lookup.

Wait-set rows (`concept:wait-set`) carry no attribute data, but the sender-run identity pinned on the receiver's own rows selects which run's persisted attributes a round-driving sender contributes: the builder resolves each round-driving sender from its pinned settled run, falling back to the most-recent fresh-settled lookup for subscribed senders that did not drive the round — so a sequenced receiver's queued rounds each substitute the inputs of their own moment (`story:sequenced-preserves-cascade-rounds`). Wait-set drain triggers gate-eval; gate-eval then reads the persisted store for sender values in scope.

## Rationale

Splits the wait-set's role cleanly: wake-vs-data. Wait-set rows drive *when* a receiver is evaluated; the persisted attribute store — scoped to the current frame — is the source of truth for *what* each sender produced in that frame's work. The two are independent — a sender's diff-based attribute cascade (`concept:cascade`) may emit no changed-signal for an unchanged value inside the frame's cascade rounds, but the receiver can still substitute against that sender's current in-frame attributes because the lookup is by node identity against the current frame's ledger rows, not by signal payload presence. Conversely, a wait-set row never delivers a snapshot of its own; it only names the settled run whose persisted row the builder reads.

The frame-scoped lookup is not a policy choice made here — it is a direct consequence of `concept:frame`'s isolation invariant. The persisted store's rows from prior frames exist for external observability and audit only; the substitution builder never consults them.

## Alternatives

- **Read from drained wait-set rows.** Key substitution on wait-set row presence. Rejected — it would tie substitution-availability to cascade-emission decisions, which under diff-based attribute cascade breaks substitution for unchanged-but-needed sender values.
- **Carry sender snapshots on cascade signals.** Include the sender's full attribute snapshot in the cascade payload, propagate it through the wait-set, deliver to the receiver. Rejected: payload propagation through the wait-set is a deliberate non-feature (`concept:signal` — wait-set rows carry no payload), and snapshots would diverge from the persisted store as soon as the sender's next run settled. Reading from the store is both simpler and more current.
