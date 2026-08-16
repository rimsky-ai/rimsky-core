---
assessment: fs-fanout-expand-folder--expand-picked-folder
subject: story:fs-fanout-expand-folder
way: expand-picked-folder
release: d977250c
outcome: held
warrant: experiment:fs-fanout-expand-folder
---
# Fanning out over the contents of one folder the filesystem store picked

A deployment ran the bundled `catalog:bundled-services/claim-producer-filesystem` over a workspace holding two candidate folders, each seeded with three matching files and one non-matching file, and a template declaring a single fan-out node whose claim is the store's pick-policy selector and whose `catalog:template-keys/nodes[].fan_out.partition_request` expands the picked folder's contents. The producer opened exactly one parent claim on one of the two folders, and the run derived its expectations from which folder was picked, so it does not depend on the choice. The split returned three sub-scopes keyed by that folder's three matching file names; the non-matching file in the same folder produced no sub-claim, and no file of the folder that was not picked appeared anywhere in the run. The parent and its three work units all settled fresh, each work unit reached an endpoint at a path carrying its own partition key, and that endpoint — which holds every request open — reported a peak of three in flight, so the units ran at the same time. The template names no file, which is the point: the author does not enumerate the folder upstream. Nine checks, none failing.

## Unverified remainder

None: the passing run demonstrates the way as promised.
