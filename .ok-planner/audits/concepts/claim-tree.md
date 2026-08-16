---
audit: claim-tree
artifact: concept:claim-tree
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:21:03Z
---

# The self-referential claim-handle tree, its two recursive walks, and its counters

Supported; all six invariants check out. The structure is one nullable self-referential column on the claim-handle table, declared to null on the parent's deletion rather than cascade, with a partial index over the non-null rows; the whole tree is created in the acquisition transaction that splits the parent, one child row per sub-scope descriptor, each bumping the parent's expected-children counter. The parent pointer is written exactly once, at insert, under a freshly generated id pointing at an already-present parent — no statement anywhere in either storage backend updates that column, which is what makes acyclicity operational as the concept says and gives each non-root row exactly one root. A sub-claim inherits the parent's intent, lifetime and realized write semantics from the insert input rather than declaring its own, so a later overlapping acquisition gets an ordinary coexistence evaluation against it. The descendant-cancel walk fires unconditionally on any abandoning outcome, inside the resolving row's own settlement and before that row is promoted or, under the ownership-bail source, deleted; it skips non-active rows and rows held by another supervisor, takes a row lock on each candidate, and recurses only into rows still active, so its frontier shrinks and the recursion is bounded by depth alone. The counter distinction the last invariant draws is real and structural: a child cancelled by the descendant walk is resolved with no parent pointer threaded into its terminal decision, so the parent-settlement step never runs and no counter is bumped, while a naturally settling or sibling-cancelled child does thread it and does bump. The parent's counters are claimant-guarded, and a supervisor settling a child of a parent it does not hold reassigns holdership to itself first, immediately before firing the parent's own resolution. Coverage spans the runtime resolution suite's multi-level recursion and sibling tests plus the fan-out forensics and lineage scenarios.
