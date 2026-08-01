---
issue: dangling-annotation-retention-sweep
kind: audit
category: unspecified
artifacts:
  - code:lib/foundation/persistence/postgres/migrations/034-node-runs-events-index-cleanup.sql
  - code:lib/foundation/persistence/sqlite/migrations/034-node-runs-events-index-cleanup.sql
status: answered
opened: 2026-07-24T00:00:00Z
---

# Do the two 034 migration files still cite a `retention-sweep` concept that was never written?

No. Both files now carry `-- @concept: node-run` and `-- @concept: event-log` — no `retention-sweep` citation is present in either file, and both cited slugs resolve to live concept documents (`.ok-planner/design/concepts/node-run.md`, `.ok-planner/design/concepts/event-log.md`). The dangling annotation the issue reported no longer exists; the migrations already follow the distributed-documentation precedent the same runtime file (`lib/runtime/retention_sweeps.go`) itself uses (`@concept: frame`, `@concept: event-log`, `@concept: claim-handle`, `@concept: claim-lifetime`, `@concept: message`, field by field).
