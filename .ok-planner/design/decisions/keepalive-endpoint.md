---
decision: keepalive-endpoint
status: as-is
aliases: []
---

# Dedicated keepalive endpoint

## Choice

A dedicated keepalive route on the supervisor, keyed by run id and authenticated with the dispatch's existing cancel token. A call carries no body, answers with the same no-content convention as the attribute-writeback callback, and has one side effect: bumping the dispatch's last-progress timestamp.

## Rationale

Async executors that do not have meaningful attribute updates need an explicit liveness primitive that does not pollute the attribute bag with dummy values. A dedicated endpoint keeps the liveness purpose distinct from the attribute-writeback purpose.

## Alternatives

Reuse the attribute writeback callback only — rejected because it forces meaningless writes for liveness. Reverse polling via a `Ping` RPC on the executor — rejected because of supervisor-side polling load and executor-side state burden.
