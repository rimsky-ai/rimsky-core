---
decision: blob-backends-pluggable
---

# Blob storage abstraction

## Choice

Pluggable backend interface across the inline, Postgres-large-object, filesystem, and memory backends (see `concept:blob-backend`).

## Rationale

Deployment-specific spill targets.

## Alternatives

- A single fixed backend (inline in the SQL row) — rejected: per-row size limits cap payloads and no deployment can pick a spill target that fits it.
