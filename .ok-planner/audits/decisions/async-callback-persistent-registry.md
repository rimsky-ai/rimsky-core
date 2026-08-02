---
audit: async-callback-persistent-registry
artifact: decision:async-callback-persistent-registry
determination: supported
commit: b767a27d
audited: 2026-08-02T09:32:02Z
---

# Async-callback registration is persisted on the dispatch row, not only in memory

Supported. `RegisterAsyncAck` (implemented for both backends, checked in `lib/foundation/persistence/postgres/queue.go`) writes `async_ack_id` and `async_ack_registered_at` directly onto the `rimsky_node_runs` row in the same UPDATE that also stamps `last_progress_at`/deadline fields, i.e. columns on the dispatch row itself rather than a side table — matching the rejected "separate callback-registry table" alternative's absence. `CallbackServer.handleCallback` falls back from the in-memory `CallbackRegistry.Pop` to a DB-backed `lookupAsyncCtxByAck` (`Queue.LookupRunByAsyncAckID` plus acquisition/attribute reconstruction) whenever the ack id is not held in memory. `lib/runtime/callback_restart_recovery_test.go` exercises exactly the restart scenario end to end: it registers an async ack directly on the dispatch row via `RegisterAsyncAck`, then posts to a freshly constructed `CallbackServer` with an empty in-memory registry (simulating a supervisor restart) and asserts the callback still resolves, settles the run, and merges attributes correctly.
