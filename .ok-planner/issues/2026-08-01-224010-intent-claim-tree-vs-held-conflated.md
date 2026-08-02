---
issue: intent-claim-tree-vs-held-conflated
kind: sprint
category: intent-ledger
artifacts:
  - concept:claim-tree
  - concept:claim-co-holdership
status: open
opened: 2026-08-01T22:40:10Z
---

# Intent ledger frames fan-out sub-claims and held co-holdership as one tree

## Problem

The claim-tree dossier (transcript tier) describes the parent-pointer claim tree as built by two mechanisms — fan-out sub-claims and held claims spanning a sub-graph. The live corpus keeps these as two distinct structures: `concept:claim-tree` owns the self-referential parent pointer (fan-out only), while co-holdership uses a separate co-holder ledger keyed by claim handle plus holder run, never the parent pointer.

Evidence tier: transcript.

## Candidates

- Retire the ledger's one-tree framing as imprecise; the corpus's two-structure model stands.
- Rule the one-tree framing the intent and reconcile the corpus.
