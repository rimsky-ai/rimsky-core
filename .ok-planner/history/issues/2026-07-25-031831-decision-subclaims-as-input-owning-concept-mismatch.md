---
issue: decision-subclaims-as-input-owning-concept-mismatch
kind: audit
category: inconsistent
artifacts:
  - decision:subclaims-as-input
status: answered
opened: 2026-07-25T03:18:31Z
---

# decisions.md TOC and decision body name different owning concepts for sub-claim acquisition

## Problem

TOC says claim-tree machinery (concept:claim-tree); body says fan-out's partition-split mechanics (concept:fan-out). Both concepts exist; the corpus disagrees with itself about which owns acquisition.

## Candidates

- Converge TOC and body on concept:fan-out
- Converge on concept:claim-tree

## Discussion

`decision:subclaims-as-input`'s own body is right: sub-claim acquisition is owned by `concept:fan-out`, not `concept:claim-tree`. This is settled inside the corpus itself, without needing to consult code, because `concept:fan-out` and `concept:claim-tree` each independently make the same claim about themselves.

`concept:fan-out`'s Boundaries: "Owns: ... the partition-split mechanics at parent-acquisition..." and its Invariants: "Sub-claim acquisition happens upstream of dispatch: the dispatch primitive receives already-acquired sub-claims and never calls the producer's split (per `concept:child-execution`)." Its Definition section walks the mechanism directly: "the producer's partition-split operation takes the parent claim handle plus a partition request and returns N sub-scope descriptors, rimsky opens N sub-claim handles in the parent-acquisition transaction."

`concept:claim-tree`'s Boundaries explicitly disclaim the same thing from the other side: "Does NOT own: claim acquisition (see `concept:claim`, `concept:claim-handle`)..." Its Definition describes the tree as a downstream *consequence* of acquisition, not the acquisition mechanism itself: "Created by fan-out: the parent's split-scope verb returns N sub-scope descriptors and rimsky inserts N child claim-handle rows in the same acquisition transaction." Its own Owns list is scoped to "the self-referential parent pointer on the claim-handle ledger, the child-listing accessor, the recursive parent-resolution walk, the recursive descendant-cancel walk" — post-acquisition bookkeeping and resolution, not the split-scope acquisition act.

So the corpus does not actually disagree with itself about which concept *should* own acquisition — `concept:fan-out` claims it and `concept:claim-tree` disclaims it, consistently. Only the `decisions.md` TOC line for this one decision names the wrong concept; the decision's own body already says `concept:fan-out`, matching both bearing concepts. (I did not additionally check code for this one, since the mismatch resolves entirely from the corpus's own self-consistent boundary declarations without needing to.)

Closing this issue as answered by `decision:subclaims-as-input`'s own body, cross-confirmed by `concept:fan-out`'s Owns/Invariants and `concept:claim-tree`'s explicit "Does NOT own: claim acquisition" disclaimer. A future sprint should regenerate the `decisions.md` TOC line to name `concept:fan-out`, not `concept:claim-tree`.
