---
issue: intent-claim-tree-vs-held-conflated
kind: sprint
category: intent-ledger
artifacts:
  - concept:claim-tree
  - concept:claim-co-holdership
status: answered
opened: 2026-08-01T22:40:10Z
---

# Intent ledger frames fan-out sub-claims and held co-holdership as one tree

## Question

Does the live corpus model fan-out sub-claims and held co-holdership as one tree structure, or as two distinct structures?

## Answer

The corpus already keeps them distinct. `concept:claim-tree`'s Boundaries own only "the self-referential parent pointer on the claim-handle ledger" and its "What it is" states the tree is "Created by fan-out: the parent's split-scope verb returns N sub-scope descriptors and rimsky inserts N child claim-handle rows in the same acquisition transaction" — fan-out only, with no mention of co-holdership. `concept:claim-co-holdership`'s Invariants independently state "the co-holder row is inserted in the co-holder's own acquire transaction, keyed by the holder run" — never via the parent pointer. The two-structure model is the corpus's existing, current commitment; only the historical intent ledger conflated them.
