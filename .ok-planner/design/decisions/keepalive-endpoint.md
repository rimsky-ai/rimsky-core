---
decision: keepalive-endpoint
status: as-is
aliases: []
---

# Dedicated keepalive endpoint

## Choice

A keepalive route on the supervisor at `/v1/runs/{run_id}/keepalive`. Authenticated via the existing `cancel_token`. No request body. Returns 204 No Content on success (matching the attribute-writeback callback's convention), 401 on auth failure, 404 on unknown run. Side effect: bumps `last_progress_at` on the dispatch row.

## Rationale

Async executors that do not have meaningful attribute updates need an explicit liveness primitive that does not pollute the attribute bag with dummy values. A dedicated endpoint keeps the liveness purpose distinct from the attribute-writeback purpose.

## Alternatives

Reuse the attribute writeback callback only — rejected because it forces meaningless writes for liveness. Reverse polling via a `Ping` RPC on the executor — rejected because of supervisor-side polling load and executor-side state burden.
