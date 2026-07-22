---
decision: blob-backend
status: adopted
---

# blob-backend

## Choice

Use the filesystem blob backend rooted under the per-run artifact layout (see `concept:blob-backend`, `decision:artifact-layout`), with the default spill threshold. The filesystem backend keeps values at or under the threshold inline in the SQL row and spills values above the threshold to a file under the root.

## Rationale

A single backend choice that delivers both properties: small values stay in the SQL row (good locality for the bulk of audit data; one file ships the run); large values get a sibling file under the run directory (no per-row size limit). The pure-inline backend forbids spill and would risk SQL per-row size limits for large payloads; the memory backend would gut the audit story; the postgres-large-object backend is not portable to sqlite.

## Alternatives

Pure-inline backend (large-payload risk; explicitly errors on values that exceed the row-size limit). The memory blob backend (audit gap; tracked separately as `issue:memory-blob-audit-gap`).
