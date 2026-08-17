---
decision: keepalive-endpoint
---

# Dedicated keepalive endpoint

## Choice

A dedicated keepalive route on the supervisor, keyed by run id and authenticated with the dispatch's existing cancel token. A call carries no body and answers with the same no-content convention as the attribute-writeback callback. It persists two effects in one transaction: it bumps the dispatch's last-progress timestamp, and it renews the expiry of every claim the run holds.

## Rationale

Async executors that do not have meaningful attribute updates need an explicit liveness primitive that does not pollute the attribute bag with dummy values. A dedicated endpoint keeps the liveness purpose distinct from the attribute-writeback purpose. Renewing the run's claim expiries in the same call completes the primitive: a dispatch long enough to need keepalives is long enough for its claim leases to fall behind the orphan reaper, and splitting the two would let a caller keep the dispatch alive while the reaper reclaims its claims underneath it.

## Alternatives

Reuse the attribute writeback callback only — rejected because it forces meaningless writes for liveness. Reverse polling via a `Ping` RPC on the executor — rejected because of supervisor-side polling load and executor-side state burden. A keepalive that touches only the progress timestamp, with claim renewal on its own route — rejected: two calls on the same cadence for one notion of "still working", and a caller that makes one and forgets the other loses its claims.
