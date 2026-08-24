---
decision: scratch-column
---

# Executor scratch persists on the node-run row

## Choice

Executor scratch persists as a byte column on the node-run row, committed with the row (see `decision:attribute-bytes-in-the-row`). Default is empty.

## Rationale

Scratch lives and dies with the dispatch row, so the row is its natural home, and a column on that row is the same idiom the attribute bag uses.

## Alternatives

- A dedicated scratch table keyed by node-run — rejected: an extra join and a second payload idiom for state whose lifecycle is exactly the dispatch row's.
