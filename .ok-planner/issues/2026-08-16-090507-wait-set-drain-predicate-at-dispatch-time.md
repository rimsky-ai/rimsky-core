---
issue: wait-set-drain-predicate-at-dispatch-time
kind: audit
category: conflicting
artifacts:
  - concept:wait-set
status: verified
opened: 2026-08-16T09:05:07Z
---

# The dispatcher still tests wait-set drain at candidate selection, which the concept says it no longer does

The wait-set concept says upstream-dependency gating happens entirely before a run reaches stale — the drain stamps the wait-set and triggers the gate evaluator — so the dispatcher's candidate selection carries only its serialization predicate. Both backends' candidate queries also carry a not-exists check for undrained wait-set rows. Whether that predicate is dead or load-bearing turns on whether any path can commit a stale row whose wait-set rows are not yet drained; the one path traced (sub-graph entry binding) drains in the same transaction and needs no defence, but the pending-to-stale transition and the sender's drain stamp are separate transactional steps, and nothing was proved about their interleaving. The ruling decides whether the predicate is documented as a defence or removed after proof.

## Options

- Document that candidate selection tests drain alongside serialization and which layer is authoritative; cost: an admittedly-maybe-redundant join stays in the hot query, explained.
- Remove the predicate after proving no path leaves a stale row with undrained wait-set rows — a targeted concurrency test in the race-injection pattern, not a reading; cost: verification work, and getting it wrong reintroduces a live ordering bug.

The ruling decides whether the dispatcher keeps its belt with the braces.

## Ruling

> Recommended ruling (/verify-issues): Keep the predicate and document it as a defence in depth — the gate evaluator is authoritative, the dispatcher re-checks — until a race-injection test proves the transition and the drain cannot interleave; if that proof lands, remove it then.
>
> Rationale: the project treats dispatcher claim ordering as a defended concurrency seam and pins such properties with injected races, not by reading; a redundant not-exists on an indexed table is cheap and a wrong removal is not. Flip case: the proof — a passing race-injection scenario over the drain/transition interleaving — is exactly the flip.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
