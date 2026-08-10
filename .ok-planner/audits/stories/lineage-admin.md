---
audit: lineage-admin
artifact: story:lineage-admin
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T07:25:00Z
---

# Pruning lineage records older than a cutoff bounds their growth

Supported. On a deployment whose run tree had aged out under short retention
while its lineage records remained, `rimsky lineage prune` with a cutoff an hour
older than every record deleted nothing and left every record readable, and with
a cutoff an hour newer deleted four rows, after which the run id answered 404 and
both the by-producer and by-source reads answered empty. A second workflow
recorded lineage again and `--older-than` pruned it by age instead of timestamp,
so the operator has a repeatable way to keep the records from accumulating. A
cutoff that is not a timestamp and a prune with no cutoff were each refused 400.
One condition governs the prune beyond the cutoff, and an operator will meet it:
a lineage row whose node run or claim handle still exists is retained whatever
the cutoff says, so on the same deployment before the retention sweep had run, a
prune with a cutoff an hour in the future deleted nothing.
