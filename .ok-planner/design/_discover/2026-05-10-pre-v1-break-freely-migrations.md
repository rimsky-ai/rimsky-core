---
topic: pre-v1-break-freely-migrations
kind: discipline
---

# Pre-v1: migration filenames append-only, migration SQL free to drop+recreate; no compat shims

## Description

A standard migration discipline never drops or rewrites past migrations — every migration is forward-only and tolerates pre-existing data. Rimsky is pre-v1 (`.claude/rules/rules.md` "Pre-v1 — break freely") and chose to spend the "no consumer is locked into a particular schema yet" budget rather than carrying compat shims through every breaking schema rethink.

Migrations live in `foundation/persistence/{postgres,sqlite}/migrations/` and are applied filename-sorted by the migration runner (`foundation/persistence/migrations.go`). The filenames themselves are append-only (the runner tracks applied filenames in `rimsky_migrations`), but the SQL inside each migration is free to drop existing tables and recreate them:

- `001-initial.sql:11-13` — "Pre-v1 break-freely: this migration was rewritten in place rather than as a successor; dev DB is nuked on adoption."
- `003-template-registry-and-lifecycle.sql:5-13` — "Pre-v1: drop and recreate templates/instances. Existing dev databases are nuked."
- `006-platform-extensions-park-blob-events.sql:5-8` — "Pre-v1: idempotent ADD COLUMN IF NOT EXISTS / CREATE TABLE IF NOT EXISTS throughout. Dev databases on the 'phase' CHECK constraint may need a nuke-and-recreate; the constraint is dropped and recreated rather than altered."

The rules file makes this explicit: "Rimsky is pre-v1. There is no production data to preserve and no consumer is locked into a particular schema. When a refactor would be cleaner without a migration path, take the clean path. Delete dead code rather than carrying it forward."

Pre-v1 also extends to other surfaces. Per `.claude/rules/rules.md`: "No backwards-compat guarantees on the wire protocol, the YAML config shape, the event-log payloads, or the resource interface until v1 ships. If a change requires nuking a dev Postgres, say so explicitly."

The migration runner uses the session advisory lock (`@blessed-invariant 8` per `2026-05-10-advisory-locks-tick-and-migrate`) so concurrent migrate runs across replicas serialize on the lock; the per-batch idempotency is the operator's responsibility.

When v1 ships, this rule flips. The `pre-v1` markers in migration files become trip-wires for any post-v1 migration that wants the same freedom — a future audit can grep `pre-v1` to find the spots that need a forward-compat path. The rules file is explicit: "When v1 ships, replace this section with deployed-stage rules."

The consequence operators see: a dev Postgres requires a wipe on schema-shape changes; the changelog and the migration file say so. Production deployment of pre-v1 rimsky is implicitly not promised — operators must accept dev-database nukes.

## Code surface

- `foundation/persistence/postgres/migrations/001-initial.sql:5-13` — initial migration's pre-v1 prologue.
- `foundation/persistence/postgres/migrations/003-template-registry-and-lifecycle.sql:5-13` — drop+recreate prologue.
- `foundation/persistence/postgres/migrations/006-platform-extensions-park-blob-events.sql:5-8` — `IF NOT EXISTS` discipline.
- `foundation/persistence/migrations.go` — runner; tracks `rimsky_migrations`.
- `foundation/persistence/postgres/advisory_locker.go:63-82` — session-level migration lock.
- `cmd/rimsky-migrate/main.go` — operator entry point.

## Prose surface

- `.claude/rules/rules.md` "Pre-v1 — break freely" section.
- `CLAUDE.md` "Non-obvious gotchas" — pre-v1 hash bytes, schema drops.
- `CHANGELOG.md` — explicit "dev DB nuke required" notes.
- `.ok-planner/specs/2026-05-04-foundation-contract.md` — migration-runner discipline.

## Adjacent topics

- `2026-05-10-content-addressed-templates` — pre-v1 hash bytes not pinned.
- `2026-05-10-advisory-locks-tick-and-migrate` — migration lock primitive.
- `2026-05-10-unified-rimsky-yml-config` — YAML config also pre-v1.

## Observations

- The `pre-v1` annotation is text in migration prologues; nothing in the build enforces it. A future post-v1 migration that wants to drop+recreate must (a) bypass the lint that catches `DROP TABLE` and (b) be explicit about the data loss. Today neither lint exists.
- The freedom to drop and recreate is mostly an early-stage pragmatism choice. The rules file's "Delete dead code rather than carrying it forward" extends the same principle into code: pre-v1 code is allowed to be replaced rather than refactored if the replacement is cleaner.
- The migration runner's append-only filename rule is structural: the runner records applied filenames and refuses to re-apply. Rewriting `001-initial.sql` doesn't help in a database that's already recorded `001-initial.sql` as applied — the runner skips it. The dev-nuke is the explicit workaround.
- The pre-v1 marker is concentrated in three places: migration prologues (visible), CLAUDE.md gotchas (operator-facing), and the rules file (project policy). A future v1-prep audit needs to grep for the marker in all three.
