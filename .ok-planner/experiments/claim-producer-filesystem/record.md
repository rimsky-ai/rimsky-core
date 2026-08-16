---
experiment: claim-producer-filesystem
commit: d977250c
---

# Claims on plain files, and fan-out over the store's own contents

## What it ran against

A `rimsky-all-in-one` stack from this tree's image with the bundled filesystem
claim producer configured over a bind-mounted host directory as its root. No
database is stood up: the stack's own state is the image's SQLite default and
the claim substrate is the host directory. Nodes run on the bundled http-node
executor, which posts its resolved attributes to a service on the host; that
service performs the node's write through the address the producer returned,
by translating the container path back to the host path.

## What was observed

The producer registers over the configured root at boot.

A node claiming the directory `data/reports` under the root received the
address `/workspace/data/reports` — the claimed directory itself — and its
claim handle records realized write semantics `sync` and state `committed`.
The executor's write landed at that address: the host directory holds the file
with the bytes the executor sent. Comparing the root's full file listing before
and after the run, the only entry the run added is the written file; the commit
created no staging directory and swapped in no copy.

A second node claimed the directory `data/inbox`, which already held three
files, and declared a fan-out whose partition request expands the folder. The
producer's split returned three sub-scopes, the dispatch's partition keys are
the three file names, and each work unit's claim addresses its own file
(`/workspace/data/inbox/alpha.txt` and the other two). The parent and all
three work units settled fresh.
