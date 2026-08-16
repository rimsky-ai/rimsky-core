---
assessment: claim-producer-filesystem--in-place-claim
subject: story:claim-producer-filesystem
way: in-place-claim
release: d977250c
outcome: held
warrant: experiment:claim-producer-filesystem
---
# Directory-per-scope claims that write in place, with no database

A deployment of `catalog:images/rimsky-all-in-one` ran with `catalog:bundled-services/claim-producer-filesystem` configured over a mounted host directory and no database beyond the image's own default. A node claiming a directory under that root received the directory itself as its claim address, and its claim handle recorded realized synchronous write semantics with the claim committed. The executor's write landed at that address on the host. That the write is in place rather than staged-and-swapped was checked by comparing the root's full listing before and after: the written file is the only entry the run added, so the commit staged nothing and swapped in no copy.

## Unverified remainder

None: the passing run demonstrates the way as promised.
