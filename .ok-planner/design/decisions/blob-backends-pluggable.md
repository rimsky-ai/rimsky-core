---
decision: blob-backends-pluggable
status: as-is
---

# Blob storage abstraction

## Choice

Pluggable backend interface (`inline`, `pg-largeobject`, `filesystem`, `memory`).

## Rationale

Deployment-specific spill targets.
