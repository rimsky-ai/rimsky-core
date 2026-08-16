---
audit: lineage-admin
artifact: story:lineage-admin
text: noncompliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:48:35Z
---

# An operator prunes lineage records older than a cutoff

Supported. Driven through the public surface against a container of the released
all-in-one image with short retention, so one workflow's run tree ages out while
its lineage records remain — the state a long-lived deployment reaches. Ten
checks, none failing. A cutoff older than every record deleted nothing and left
every record readable; a cutoff newer than the records deleted four rows, after
which the run id answered not-found and both the by-producer and by-source reads
answered empty. Work run after the prune recorded lineage again, and the CLI's
age form of the cutoff deleted those rows too. Both malformed inputs were refused
rather than guessed at: a cutoff that is not a timestamp came back naming the
format it wanted, and a prune with no cutoff came back naming the missing field.
The probe prunes only after the run tree has aged out, and this run took no
separate measurement of a prune issued before that point.

## Compliance

- The benefit clause names storage — "the lineage table doesn't grow unbounded" prescribes the persistence structure, which decisions own; the compliant benefit is that lineage records do not grow unbounded in a long-lived deployment.
