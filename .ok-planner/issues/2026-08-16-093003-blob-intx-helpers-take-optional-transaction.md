---
issue: blob-intx-helpers-take-optional-transaction
kind: audit
category: inconsistent
artifacts:
  - decision:intx-suffix-convention
status: verified
opened: 2026-08-16T09:30:03Z
---

# Two blob helpers carry the in-transaction suffix while treating the transaction as optional

A naming decision separates two jobs: a method that runs inside a caller's transaction carries the in-transaction suffix and never runs without one; a method that optionally opens one gets the plain spelling with an optional transaction parameter — and a fitness test forbids a method coexisting with an in-transaction twin. Two package-level blob helpers carry the suffix and take an optional transaction, falling back to the plain path when it is nil — the exact optional-parameter job the decision assigns the other spelling — and the fitness test never sees them because it inspects only methods with receivers. The ruling renames them and widens the test.

## Options

- Rename the two helpers to the plain spelling with the transaction as an optional trailing parameter, update the call sites, and widen the fitness test to every top-level declaration; cost: a bounded mechanical rename.
- Add a third "dispatcher" spelling to the decision; cost: new vocabulary for a shape the decision already names.

The ruling applies the naming rule the decision already states.

## Ruling

> Generated ruling (/verify-issues): Rename the two blob helpers to the plain spelling — a single function taking an optional transaction — and widen the pair-detecting fitness test's population from receiver-bearing methods to every top-level persistence declaration. Forced by the naming decision's own Choice, which already names this shape and its spelling. Verified against the tree as it stands; nothing was applied.

<!-- Owner: this is a generated ruling, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
