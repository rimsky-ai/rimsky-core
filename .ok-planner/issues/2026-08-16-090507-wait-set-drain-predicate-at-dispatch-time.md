---
issue: wait-set-drain-predicate-at-dispatch-time
kind: audit
category: conflicting
artifacts:
  - concept:wait-set
status: verified
opened: 2026-08-16T09:05:07Z
---

# The dispatcher tests wait-set drain at candidate selection, which the concept says it no longer does

The wait-set concept says all upstream-dependency gating happens before a run reaches stale: the drain stamps the wait-set and triggers the gate evaluator, so the dispatcher's candidate selection carries only its serialization predicate. Both backends' candidate queries also carry a not-exists check for undrained wait-set rows. One question decides whether that predicate is dead or load-bearing: can any path commit a stale row whose wait-set rows are not yet drained? The one path traced, sub-graph entry binding, drains in the same transaction and needs no defence. The pending-to-stale transition and the sender's drain stamp are separate transactional steps, and nobody has proved anything about their interleaving. The ruling decides whether the corpus documents the predicate as a defence or the project removes it after proof.

## Options

- Document that candidate selection tests drain alongside serialization, and name the authoritative layer; cost: a possibly redundant join stays in the candidate query, explained.
- Remove the predicate after proving no path leaves a stale row with undrained wait-set rows, proved by a targeted concurrency test in the race-injection pattern rather than by a reading; cost: verification work, and a wrong result reintroduces a live ordering bug.

The ruling decides whether the dispatcher keeps its redundant check.

## Ruling

> Recommended ruling (/verify-issues): Keep the predicate and document it as a defence in depth. The gate evaluator is authoritative and the dispatcher re-checks. Keep it until a race-injection test proves the transition and the drain cannot interleave, and remove it when that proof lands.
>
> Rationale: the project treats dispatcher claim ordering as a defended concurrency seam and pins such properties with injected races rather than by reading. A redundant not-exists on an indexed table is cheap, and a wrong removal is not. Flip case: a passing race-injection scenario over the drain/transition interleaving is the flip.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
