---
issue: stale-recovery-duplicate-work-started-events
kind: audit
category: bug
artifacts:
  - story:work-completed-emitted
status: promoted
opened: 2026-08-02T09:58:08Z
sprint: 2026-08-03-audit-gap-drain.md
---

# A liveness-recovered dispatch emits two work-started events against one work-completed

Anything consuming rimsky's event log to meter or trace work can double-count. The story promises every work-started event pairs 1:1 with a work-completed (`story:work-completed-emitted`), but the liveness-recovery sweep breaks the pairing: when a dispatch goes quiet past its deadline, the sweep (`code:lib/runtime/conductor.go::SweepExecutorDeadlines`) resets the same node-run row back to its pre-dispatch acquisition-eligible state rather than creating a new row. The acquire loop (`code:lib/runtime/runner_acquire.go::tryAcquireBatch`) then re-dispatches it and unconditionally appends another work-started event — it has no way to tell a genuinely new dispatch from a stale-recovered reacquisition of the same dispatch id. One recovery, two work-starteds, one eventual work-completed.

The sibling emission paths are already guarded and tested: in-place retry and park-resume both assert a singleton work-started per dispatch. Only the liveness-recovery path double-emits, and no test asserts the count across it. The corpus needs no change — the story is right; the code is wrong. The ruling decides which dedup mechanism carries the fix.

## Options

- Gate the work-started append on the row's recovery disposition (the row already carries a stamp saying it came back through the sweep). Cost: correctness rides a state-machine read that future dispositions must keep in mind.
- Enforce uniqueness structurally — an existence check or constraint keyed on the dispatch id at emission time. Cost: a per-acquisition query (or schema constraint) on a hot path, for a duplicate class only one code path can produce.

## Ruling

> Recommended ruling (/verify-issues): fix the code — gate the work-started emission on the row's recovery disposition, and add a scenario test asserting the singleton work-started count across the liveness-recovery path, next to the existing retry and park singleton tests. The story stands as written.
>
> Rationale: the disposition stamp already exists and names exactly the condition that causes the duplicate, so the gate is the smallest change that restores the 1:1 promise; the structural option buys generality against duplicate classes that don't currently exist, at a hot-path cost. Flip case: if a second duplicate-emitting path ever appears (or the disposition vocabulary grows enough that the gate gets brittle), switch to the dispatch-id-keyed uniqueness check and let the schema carry the invariant.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
