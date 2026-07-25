---
issue: dangling-annotation-retention-sweep
kind: audit
category: unspecified
artifacts:
  - code:lib/foundation/persistence/postgres/migrations/034-node-runs-events-index-cleanup.sql
  - code:lib/foundation/persistence/sqlite/migrations/034-node-runs-events-index-cleanup.sql
status: verified
opened: 2026-07-24T00:00:00Z
---

# Two migration files cite a design document that was never written

Rimsky's code links itself to its design corpus via citation comments, and a lint requires every citation to resolve to a real document. Two database migration scripts (the numbered, append-only schema changes for Postgres and SQLite) both cite a concept named `retention-sweep` — and no such document exists, live or archived. The behavior they're citing is real: a periodic background job, wired into the scheduler's tick, that prunes old trace and event-log rows past a configured retention window; the migration adds an index to make that pruning fast. The question is what the citation should point at.

What re-verification found shapes the answer: the pruning job is already documented — piecemeal, inside the concept documents for each kind of data it prunes (trace frames, the event log) — and the Go file implementing it already cites exactly those concepts, field by field. So the closest thing to a prior ruling is "distributed documentation, no single home," made in code but never written down. A complication for the write-the-missing-concept option: the phrase "the retention sweep" is *already used informally* elsewhere in the corpus for a different mechanism (expiring abandoned work claims, not aging out history), so a new document with this exact name would collide with existing usage unless something gets renamed.

## Options

- **Match the code's own precedent**: repoint both migrations to the frames and event-log concepts that already describe this behavior. Zero new writing; the mechanism keeps having no single named home.
- **Author the missing concept** — one discoverable home, at the cost of the naming collision and deciding how it coexists with the other "retention sweep."
- **Repoint at the failure-cleanup concept** (the other sweep) — cheapest single target, but blurs a distinction that document deliberately draws.
- **Drop the citation** — simplest, and abandons a real design-owned code path with no corpus link.

The ruling decides: own concept or distributed; if distributed, which concepts the migrations cite; if a new concept, how the name collision resolves.

## Ruling

> Recommended ruling (/recommend-rulings): Keep the retention sweeps'
> documentation distributed. Repoint the two 034 migrations to
> @concept: frame + @concept: event-log, matching
> retention_sweeps.go's own citations for the field those indexes
> serve.
>
> Rationale: Zero new corpus content, consistent with the one prior-
> art call already made for exactly this mechanism; a new concept
> would collide with the existing informal use of 'retention sweep'
> for the claim-handle expiry sweep.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
recommended batch at sign-off. Edit the text to redirect, empty
the section to discuss live, or delete this note to adopt the
ruling as your own. -->
