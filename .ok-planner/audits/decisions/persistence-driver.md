---
audit: persistence-driver
artifact: decision:persistence-driver
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:39:53Z
---

# One-shot self-hosting runs use the SQLite adapter under the per-run artifact directory, with no in-memory variant

Supported. `persistence.Open` (`lib/foundation/persistence/open.go`) dispatches only on `Driver == "postgres"` or `"sqlite"`, erroring on anything else — checked the full switch and both registration call sites (`RegisterPostgres`/`RegisterSQLite`) and found no third, in-memory `persistence.Database` implementation registered anywhere in the module. The one-shot self-hosting run's synthetic config writer (`cmd/rimsky/cli/compose/synthetic_config.go`, tagged `@decision: persistence-driver`) sets `persistence.driver: sqlite` with the SQLite path at `<run-dir>/state.db`, i.e. under the per-run artifact directory, matching the choice exactly.
