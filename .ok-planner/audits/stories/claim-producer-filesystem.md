---
audit: claim-producer-filesystem
artifact: story:claim-producer-filesystem
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:22:00Z
---

# Claims on a plain filesystem, with fan-out over the store's own contents

Supported. A stack from this tree ran with the bundled filesystem claim
producer configured over a bind-mounted host directory and no database beyond
the image's own SQLite default, and both ways the story names were taken
through the template surface and the control API. A node claiming a directory
under the root received that directory itself as its claim address, its claim
handle recorded realized write semantics of sync and state committed, and the
executor's write landed at that address on the host: comparing the root's full
listing before and after, the written file is the only entry the run added, so
the commit staged nothing and swapped in no copy. A second node claimed a
directory already holding three files and declared a fan-out over it; the
producer's split returned three sub-scopes, the partition keys are the three
file names, each work unit's claim addresses its own file, and the parent and
all three work units settled fresh.
