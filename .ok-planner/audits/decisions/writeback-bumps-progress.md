---
audit: writeback-bumps-progress
artifact: decision:writeback-bumps-progress
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:33:46Z
---

# Attribute writeback bumps the dispatch row's progress timestamp in the same transaction

Supported. `CallbackServer.handleAttributeWriteback` (`lib/runtime/attribute_writeback.go`) applies the attribute delta (`Upsert` or `MergeDelta`) and calls `Queue.BumpLastProgressAt` inside the same `Persist.Transaction` callback, with no separate keepalive call required. `TestAttributeWriteback_AppliesDeltaAndBumpsProgressInOneTx` (`lib/runtime/attribute_writeback_test.go`) nulls out `last_progress_at`, posts a writeback, and asserts both the attribute delta landed and `last_progress_at` is non-null afterward, then posts a second writeback and confirms delta-merge semantics — directly exercising the one-transaction claim.
