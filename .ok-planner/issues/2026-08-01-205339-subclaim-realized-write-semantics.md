---
issue: subclaim-realized-write-semantics
kind: human
category: conflicting
artifacts:
  - concept:claim
  - concept:claim-tree
status: open
opened: 2026-08-01T20:53:39Z
---

# Do sub-claim rows inherit the parent's realized write semantics at insert, or deliberately carry none?

## Problem

The intent dossiers for claim and claim-tree (transcript tier, 2026-06-18) record that a sub-claim inherits BOTH the parent's intent AND its realized_write_semantics at insert time — "a sub-claim is a claim," with neither value carried on the SubScopeDescriptor wire shape because insert-time inheritance covers both.

Code today honors only half of that: AcquireSubClaims (lib/runtime/runner_subclaim.go) requires and stamps ParentIntent on every sub-claim row, but its ClaimHandleInsertInput leaves RealizedWriteSemantics empty — the only stamp site in the tree is the regular open path (lib/runtime/runner_acquire_claims.go, from the producer's open response), which sub-claims never traverse (they come from split-scope). Consequence: a later acquisition candidate whose scope conflicts with an active sub-claim row does not get a coexistence evaluation; evaluateClaimScopeConflict fails the acquisition with "claim handle … has no realized write-semantics yet (holder open still in flight)" for as long as the sub-claim is active.

The live corpus is silent: neither concept:claim, concept:claim-tree, nor concept:write-semantics says anything about a sub-claim row's realized-write-semantics value, so it cannot arbitrate. Either the dossier's inheritance-at-insert is the standing commitment and the empty column is drift, or the code's shape is deliberate (a sub-scope's coexistence story is governed by the parent row and sibling-disjointness, and the loud failure against a conflicting outside candidate is intended) and the dossier is stale.

## Candidates

- Restore the dossier's intent: AcquireSubClaims stamps the parent's realized write semantics onto every sub-claim row at insert (mirroring intent inheritance), so the coexistence predicate evaluates normally against active sub-claim holders; record the inheritance in concept:claim-tree.
- Ratify the code: sub-claim rows deliberately carry no realized value; conflicting outside candidates fail loudly rather than coexist while a fan-out is in flight; amend concept:claim-tree (and the write-semantics adjacency) to record that shape.
