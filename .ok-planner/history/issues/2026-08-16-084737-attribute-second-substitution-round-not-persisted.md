---
issue: attribute-second-substitution-round-not-persisted
kind: audit
category: conflicting
artifacts:
  - concept:attribute
status: promoted
opened: 2026-08-16T08:47:37Z
sprint: 2026-08-18-recommended-intake-drain.md
---

# The dispatcher does not persist the second attribute-substitution round

Rimsky resolves attributes, a node's typed inputs, in two rounds. The first round runs at gate time, when the node becomes eligible. The second round runs at dispatch time, when rimsky fills in claim-scoped values, schema defaults and override layers. The attribute concept says each round commits in its own transaction and the dispatcher loads the persisted second-round bag. Rimsky persists the first round only. The dispatcher loads the first round's bag, completes it in memory, and writes an event-log row saying it happened. The completed bag reaches the ledger when the run settles. The ruling decides whether the concept describes the code or the code persists the second round.

Anyone reading the attribute ledger for an in-flight run sees the gate-time bag, not what the executor received. Nothing in the corpus or the tests records the non-persisting shape as a deliberate trade-off.

## Options

- Rewrite the invariant: the first round persists, the second completes in memory, and the ledger takes the completed bag at settle; cost: in-flight observability stays reduced.
- Persist the second round at dispatch, before execution; cost: one more write per node-run on the dispatch path.
- Restate the rule as one persisted gate-time resolution plus an in-memory completion; cost: the same as the first option, in different words.

The ruling decides whether in-flight runs get an accurate ledger.

## Ruling

> Recommended ruling (/verify-issues): Rewrite the invariant to describe the current shape and keep the second round in memory. The completed bag is what the executor received, and it reaches the ledger at settle, which is when the forensic story reads it.
>
> Rationale: no story reads a mid-flight bag, and the second round runs on the execution path. The corpus should describe the tested behaviour, not add a write to match a sentence. Flip case: a debugging need to see the exact dispatched inputs of a stuck run, before it settles, is the case for persisting at dispatch. If the diagnostics story grows that clause, take the second option.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
