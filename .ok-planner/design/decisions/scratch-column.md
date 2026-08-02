---
decision: scratch-column
---

# Executor scratch persists on the node-run row

## Choice

Executor scratch persists on the node-run row as an inline-bytes-or-spilled-handle payload, following the same `concept:blob-backend` spill pattern as the row's other inert payloads. Default is empty.

## Rationale

Reuses the inert-payload persistence idiom, so persistence-layer code stays uniform, and the same blob-backend abstracts inline versus spilled storage. Scratch lives and dies with the dispatch row, so the row is its natural home.

## Alternatives

- A dedicated scratch table keyed by node-run — rejected: an extra join and a second payload idiom for state whose lifecycle is exactly the dispatch row's.
