---
experiment: dry-run-request-flag
commit: PENDING
---

# Every write action submitted with the per-request dry-run flag

## What it ran against

A `rimsky-all-in-one` container from this tree's image set with authentication
enabled and an admin key, on a private network with a claim producer that
advertises both the claim-producer and data-processing protocols — the one built
for `asset-management`, compiled for Linux and run in an `alpine` container. The
population is the write half of the control API's action registry: 23 actions.
For each one the probe sends the real request with `?dry_run=true`, requires the
synthetic envelope back, re-reads the state that write would have changed and
requires it unchanged, then sends the same request live so that an envelope
obtained by failing validation cannot pass for a preview. Preconditions are built
through the public API: a breakpoint hit is produced by installing a
`before_dispatch` breakpoint before waking an instance, a failed node by running
a template whose bundled shape check fails, and an asset by running a template
whose node opens a `lifetime: durable` claim on the data-processing producer.

At this tree the experiment was repaired: it previously booted a bare
`rimsky-all-in-one` with no claim producer, so no asset ever existed and
`asset:delete` could not be exercised. The stack now carries the producer and the
probe materializes the asset it then previews the deletion of.

## What was observed

All 23 write actions returned a synthetic envelope naming exactly one
`would_have_*` intent, left the world unchanged, and were accepted live
afterwards: template register/deploy/undeploy/deregister, tag create/set/delete,
instance create/pause/resume/kill/terminate/debug-override, breakpoint
create/delete/resume, node reset, message send, lineage prune, auth key
create/rotate/revoke, and asset delete.

RESULT: 23 pass, 0 fail, 0 not exercised
