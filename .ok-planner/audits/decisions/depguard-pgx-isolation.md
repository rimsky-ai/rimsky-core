---
audit: depguard-pgx-isolation
artifact: decision:depguard-pgx-isolation
determination: supported
commit: b767a27d
audited: 2026-08-02T09:38:50Z
---

# pgx is confined to persistence, postgres-backed services, cmd, and test scaffolding

Supported. The `.golangci.yml` `pgx-isolation` rule denies `jackc/pgx/v5` (and its `pgxpool`/`pgconn` subpackages) everywhere except a fixed negated-globs allow-list, and a repo-wide grep for `jackc/pgx` across every `.go` file in the tree found imports in exactly those allowed locations plus nowhere else: `lib/foundation/persistence/postgres/**` and its two test-support siblings `lib/foundation/pgpool` and `lib/foundation/internal/pgtest` (both are testcontainers-based Postgres test helpers, fitting the decision's "test-support" bucket even though they live under the foundation module directory), `cmd/rimsky` (blob-backend conformance), all four `lib/services/sensors/*` state-DB files and the `lib/services/subscribers/openlineage` subscriber (each backed by Postgres for state), `lib/services/claim_producers/postgres`, and the various `test/support/...`, `test/smoke`, and `lib/services/test/scenarios` harness packages. No file outside this set imports pgx, and 257 files across `lib/graph`/`lib/runtime`/`lib/control` instead import the `lib/foundation/persistence` interface package, matching "everything else consumes the persistence interfaces."
