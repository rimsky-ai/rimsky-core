---
decision: attribute-bytes-in-the-row
---

# Attribute bytes commit with the row

## Choice

Rimsky stores an attribute bag and a node-run's scratch whole in a byte column of their own row, whatever their size, and they commit in the row's transaction. The engine's own large-value handling carries the bytes, and the engine's per-value cap is the only ceiling: a write over it fails at the write with an error naming the node run, the attribute or scratch, and the byte count. Rimsky sets no threshold of its own, spills nothing to a store outside the row, and keeps no ledger of bytes to clean up.

## Rationale

A store outside the row's transaction cannot be cleaned up without a durable record of intent: after a crash, an expired stage cannot tell a commit from a rollback, so every such store needs an orphan ledger, a retention window, and a sweep, and none of them closes the gap. Bytes in the row have nothing to coordinate. Both engines rimsky ships cap a value at one gigabyte, and an attribute near that size is a design problem in the template, not a storage problem in rimsky. A rimsky-side limit below the engine's would be a second cap with a second error to explain; one above it is unreachable. A deployment that held values behind a spill handle re-creates its instances; no importer carries them over.

## Alternatives

- A transactional large-object store inside the engine — rejected: no caller streams a value, and it adds a transaction-scoped handle to every reader.
- An external blob store with an orphan queue and a sweep — rejected: the queue is the cleanup this choice removes, and it cannot be made exact.
- A rimsky-side size limit below the engine's cap — rejected: a second cap with its own error, and the policy it would carry belongs to a pluggable attribute store, which is a later design.
- A one-shot importer for values already spilled — rejected: pre-v1, re-creating instances is cheaper than a migration path nothing ships against.
