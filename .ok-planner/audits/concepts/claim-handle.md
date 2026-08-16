---
audit: claim-handle
artifact: concept:claim-handle
text: compliant
implementation: unsupported
commit: PENDING
audited: 2026-08-16T04:46:57Z
checked: 18
unaccounted: 1
---

# The claim-handle ledger: its row shape, its guard discipline, and the held variant

Unsupported, on the guard universal alone; everything else holds. The row carries every field the concept describes — lock identity, claim scope, expiry, held marker, realized write semantics, the self-referential parent pointer, the snapshotted aggregation policy with its three child counters, the lifetime selector, the version identifier, the producer candidate handle — and the two check constraints the concept cites exist verbatim in both backends, one requiring a holder reference on active rows and one forbidding it on settled ones. The state column is a closed three-value enum with a resolved-at timestamp; the node-run reference is declared to null on the parent's deletion while the holding-node reference cascades, which is the structural-deletion shape described. Non-active deletion is absence-guarded with exactly the row-discovery filters stated: the retention sweep takes committed-subgraph or abandoned rows past the cutoff with a null holder, excluding committed-durable, and the asset release path takes a single resolved row. The orphan reaper lists only active expired rows, deletes them claimant-guarded, and calls no producer verb anywhere. The held variant checks out end to end: seven node-run states with held among them, one holder row per member keyed by holder run, a locking read serializing the fire-once resolution with a race scenario over it, the strict all-completed/any-failed aggregate, the poison rule failing every still-active holder row before the resolution check so abandon fires at the first failure, and the member/non-member routing implemented as two complementary filters over holding-subgraph membership — the immediate walk to members at the held terminal, the deferred walk to non-members at commit or abandon. The defect is the first invariant's universal. Enumerating every mutating operation the ledger interface exposes gives eighteen; three delete only settled rows and are absence-guarded by design, one inserts, and thirteen of the remaining fourteen active-row mutations carry the holding-supervisor predicate. One does not, in both backends, and the invariant closes off the reading that would excuse it by stating there is no field-repoint carve-out.

## Unaccounted

- The holder-run expiry renewal repoints the expiry field on active rows keyed only by the holding node-run, with no holding-supervisor predicate, in both backends.
