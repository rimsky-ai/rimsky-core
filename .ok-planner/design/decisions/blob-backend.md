---
decision: blob-backend
---

# blob-backend

## Choice

Use the filesystem blob backend rooted under the per-run artifact layout (see `concept:blob-backend`, `decision:artifact-layout`), with the default spill threshold. The filesystem backend keeps values at or under the threshold inline in the SQL row and spills values above the threshold to a file under the root.

## Rationale

A single backend choice that delivers both properties: small values stay in the SQL row (good locality for the bulk of audit data; one file ships the run); large values get a sibling file under the run directory (no per-row size limit).

## Alternatives

- The pure-inline backend — rejected: forbids spill and explicitly errors on values that exceed the row-size limit, a large-payload risk.
- The memory blob backend — rejected: guts the audit story.
- The Postgres-large-object backend — rejected: not portable to sqlite.
