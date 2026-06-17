---
decision: async-callback-persistent-registry
status: as-is
aliases: []
---

# Async-callback registry persists across supervisor restart

## Choice

The dispatch row carries an `async_ack_id` and an `async_ack_registered_at` timestamp; the `async_ack_id` is indexed for the callback handler's lookup. On AwaitAsyncCallback the supervisor writes the registration in the same transaction as the dispatch-state mutation; on callback the handler looks up the dispatch row by `async_ack_id`.

## Rationale

With AwaitAsyncCallback as a primary dispatch mode, the in-memory registry's restart-fragility is unacceptable. A callback arriving after a supervisor restart must land on the correct dispatch row; an in-memory map cannot survive process death.

## Alternatives

Separate `rimsky_async_callbacks` table — rejected because a column on the dispatch row is sufficient and avoids a cross-table join on the hot lookup path.
