---
issue: promotion-version-id-never-reaches-lineage
kind: audit
category: conflicting
artifacts:
  - concept:data-processing
status: verified
opened: 2026-08-16T08:59:36Z
---

# A promotion's version identifier reaches the claim handle but never the lineage ledger

A data-processing producer answers a commit with a version identifier for the promoted asset; the data-processing concept says rimsky records it on the claim handle and in the lineage ledger. The lineage record is written inside the settlement transaction from the claim handle's version at that moment; the identifier only arrives later, with the deferred commit response the outbox delivers, and is stamped on the handle then — and the lineage table has no update path, by the lineage concepts' own append-only invariant. So the version always reaches the handle and never the ledger, and nothing tests it. The ruling decides which half of the promise moves.

## Options

- Narrow the invariant to the claim handle and point consumers there for the version; cost: an audit-trail promise shrinks.
- Defer the lineage write for data-processing promotions until the commit response arrives; cost: a sequencing change to settlement.
- Add an update path so the deferred handler stamps the row; cost: breaks the lineage ledger's append-only invariant, stated in two concepts.

The ruling decides where a promotion's version is recorded.

## Ruling

> Recommended ruling (/verify-issues): Defer the lineage record for a data-processing promotion until the commit response lands, writing it once with the version it now knows — keep the ledger append-only and the concept's promise intact.
>
> Rationale: two concepts already forbid updating lineage rows, and the version is exactly what a lineage consumer of a promotion wants; a late insert costs an ordering change, an early insert costs the fact. Flip case: if lineage consumers must see the record at settlement time regardless of version (a dashboard reading "what happened" before the producer answers), narrow the invariant to the handle and say the ledger records the promotion without its version.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
