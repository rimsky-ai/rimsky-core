---
issue: concept-persistence-database-pre-v1-migration-framing
kind: audit
category: unclear
artifacts:
  - concept:persistence-database
status: answered
opened: 2026-07-25T03:18:31Z
---

# migration-discipline invariant labeled 'Pre-v1'

## Problem

'Pre-v1 migration discipline: filenames are append-only; SQL inside is free to drop+recreate' frames the discipline as stage-bound; whether the discipline survives v1 is unstated.

## Candidates

- Drop the qualifier if append-only-with-free-SQL is permanent
- Keep the stage-bound framing but state the post-v1 discipline explicitly when v1 ships (rules.md already promises a deployed-stage rewrite)

## Discussion

The corpus already answers this by splitting what `concept:persistence-database`'s invariant conflates into one "Pre-v1" clause: "Pre-v1 migration discipline: filenames are append-only; SQL inside is free to drop+recreate."

Two separate decisions back these two claims, and they carry different lifetimes:

- `decision:migrations-append-only-numbered` — Choice: "Numerically ordered, append-only, per backend." Rationale: "Migration-runner shape; ordering is the runner's contract." `status: as-is`. No pre-v1 framing anywhere in the decision — it is presented as a durable property of how the migration runner works (append-only ordering is what lets one shared runner sequence migrations across the Postgres and SQLite adapters without forking), not a stage-bound concession. The decisions catalog cross-references it directly to `concept:persistence-database`.
- `decision:migrations-no-compat-shims` — Choice: "Drop + recreate when cleaner; no compat shims." Rationale: "Pre-v1 (see `decision:pre-v1-break-freely`)." This decision is explicitly and only pre-v1-scoped — its own Rationale says so by name, tying it to the general pre-v1 stance (`decision:pre-v1-break-freely`: "No production data yet; cleaner refactors"), which `rules.md`'s "Pre-v1 — break freely" section explicitly promises to replace ("When v1 ships, replace this section with deployed-stage rules").

So the append-only-filenames half of the concept's invariant is durable architecture (the runner's ordering contract, unrelated to pre-v1 status), while the free-to-drop-recreate-SQL half is explicitly and only a pre-v1 allowance that lapses when `decision:pre-v1-break-freely` is retired at v1. The concept's single "Pre-v1 migration discipline: ..." sentence blankets both halves with one qualifier that only actually applies to the second. This resolves the issue's Problem ("whether the discipline survives v1 is unstated") for both halves without requiring interpretation: one survives (by the backing decision's own text), one is explicitly staged (by the backing decision's own text).

This is a corpus-drift finding on the concept's wording, not an open question for the owner — the fix is mechanical once the two backing decisions are read together. Filed as `answered` rather than `verified` because no judgment call remains: the next `/plan-sprint` (or a direct edit) should split the invariant into its two constituent claims, dropping "Pre-v1" from the append-only clause and keeping it (or making it more explicit) on the drop-recreate clause.

