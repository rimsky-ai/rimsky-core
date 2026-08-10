---
audit: claim-producer-filesystem
artifact: story:claim-producer-filesystem
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T07:45:00Z
---

# Production claim semantics on plain files, with no database stood up

Supported. A stack booted from this tree's image with the bundled filesystem
claim producer configured over a bind-mounted host directory, and no database
was stood up for the claims. A node claiming the directory `data/reports` under
the root received that directory itself as its address, its claim handle
recorded realized write semantics `sync` and state `committed`, and the write
its executor performed through that address is present at the same path on the
host. Comparing the root's full listing before and after the run, the only entry
the run added is the written file: the commit created no staging directory and
swapped in no copy. A second node claimed a directory already holding three
files and declared a fan-out that expands the folder; the producer returned
three sub-scopes, the partition keys are the three file names, each work unit's
claim addresses its own file, and the parent and all three work units settled
fresh.
