---
decision: async-callback-persistent-registry
status: as-is
aliases: []
---

# Async-callback registry persists across supervisor restart

## Choice

The async-callback registration is persisted on the dispatch row itself — an acknowledgement id and registration timestamp, written in the same transaction as the dispatch-state mutation — and the callback handler resolves an arriving callback to its dispatch by that id.

## Rationale

With AwaitAsyncCallback as a primary dispatch mode, the in-memory registry's restart-fragility is unacceptable. A callback arriving after a supervisor restart must land on the correct dispatch row; an in-memory map cannot survive process death.

## Alternatives

- An in-memory registration map — rejected: does not survive supervisor restart, so late callbacks orphan.
- A separate callback-registry table — rejected: columns on the dispatch row are sufficient and avoid a cross-table join on the hot lookup path.
