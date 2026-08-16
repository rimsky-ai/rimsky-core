---
issue: attribute-second-substitution-round-not-persisted
kind: audit
category: conflicting
artifacts:
  - concept:attribute
status: verified
opened: 2026-08-16T08:47:37Z
---

# The second attribute-substitution round is not persisted before dispatch

Attributes (a node's typed inputs) are resolved in two rounds: at gate time, when the node becomes eligible, and at dispatch time, when claim-scoped values, schema defaults and override layers are filled in. The attribute concept says each round is committed in its own transaction and the dispatcher loads the persisted second-round bag. The first round is persisted; the second is not — the dispatcher loads the first round's bag, completes it in memory, writes only an event-log row saying it happened, and the completed bag first reaches the ledger when the run settles. The ruling decides whether the concept describes reality or the code persists the second round.

Who observes it: anyone reading the attribute ledger for an in-flight run sees the gate-time bag, not what the executor actually received. Nothing in the corpus or tests records the non-persisting shape as a deliberate trade-off.

## Options

- Rewrite the invariant: the first round persists, the second completes in memory, the ledger sees the completed bag at settle; cost: in-flight observability stays reduced.
- Persist the second round at dispatch, before execution; cost: one more write on the hot dispatch path per node-run.
- Reframe as one persisted gate-time resolution plus an in-memory completion; cost: same as the first, with different words.

The ruling decides whether in-flight runs get an accurate ledger.

## Ruling

> Recommended ruling (/verify-issues): Rewrite the invariant to describe the current shape and keep the second round in memory — the completed bag is what the executor received, and it is on the ledger at settle, which is when the forensic story reads it.
>
> Rationale: no story reads a mid-flight bag, and the dispatch path is the hot one; the corpus should describe the tested behaviour, not add a write to match a sentence. Flip case: a debugging need to see the exact dispatched inputs of a stuck run (before it settles) is the case for persisting at dispatch — if the diagnostics story grows that clause, take the second option.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
