---
experiment: fs-fanout-expand-folder
commit: d977250c
---

# Fanning out over a picked folder's contents against the filesystem store

## What it ran against

`run.py` starts a concurrency-observing endpoint on the host that holds each
request open and reports the peak number in flight, then boots a
`rimsky-all-in-one` container from this tree's image with the bundled filesystem
claim producer configured over a bind-mounted workspace. The producer's config
declares a pick policy over a folder root holding two candidate folders, each
seeded with three `.txt` files and one `.md` file, all named so the two folders'
contents are distinguishable. The template declares one fan-out node whose claim
is that pick-policy selector and whose `partition_request` is
`{"expand_folder": {"filter": "*.txt"}}`; each work unit posts to the endpoint
at a path carrying its own partition key. The script reads which folder the
producer picked off the claim-handle ledger and derives its expectations from
that, so the run does not depend on which candidate the pick policy chose.

## What was observed

The producer opened exactly one parent claim, and its scope named one of the two
candidate folders. The split returned three sub-scopes, the dispatch recorded
sub-claims keyed by that folder's three `.txt` file names, the `.md` file in the
same folder produced no sub-claim, and no file of the folder that was not picked
appeared anywhere in the run. All four runs — the parent and its three clones —
settled fresh. The endpoint received one request per file, each at the path
carrying that file's partition key, and reported a peak of three requests in
flight, so the work units ran at the same time. The template enumerated no file
names.

Nine checks, none failing.
