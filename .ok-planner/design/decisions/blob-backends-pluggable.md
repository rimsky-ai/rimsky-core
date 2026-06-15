---
decision: blob-backends-pluggable
status: as-is
---

# Blob storage abstraction

## Choice

Pluggable backend interface across the inline, Postgres-large-object, filesystem, and memory backends (see `concept:blob-backend`).

## Rationale

Deployment-specific spill targets.
