---
story: idempotent-mode-dedupes
---

# Idempotent modes drop re-runs whose inputs equal a predecessor

## Story

As a template author whose executor is expensive and whose inputs are sometimes byte-identical across cascade rounds, I can opt a node into an idempotent cascade mode (`concept:cascade-mode`) — comparing against the queued predecessor only, or also against the most recent settled run — so that re-runs with identical inputs are dropped before reaching my executor and its work is never wasted on inputs it already processed.
