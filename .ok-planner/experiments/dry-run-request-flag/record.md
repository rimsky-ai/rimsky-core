---
experiment: dry-run-request-flag
commit: PENDING
---

# Every write action submitted with the per-request dry-run flag

## What it ran against

A `rimsky-all-in-one` container from this tree's image set with authentication
enabled and an admin key. The population is the write half of the control API's
action registry: 23 actions. For each one the probe sends the real request with
`?dry_run=true`, requires the synthetic envelope back, re-reads the state that
write would have changed and requires it unchanged, then sends the same request
live so that an envelope obtained by failing validation cannot pass for a
preview. Preconditions are built through the public API: a breakpoint hit is
produced by installing a `before_dispatch` breakpoint before waking an instance,
and a failed node by running a template whose bundled shape check fails.

## What was observed

22 of the 23 write actions returned a synthetic envelope naming exactly one
`would_have_*` intent, left the world unchanged, and were accepted live
afterwards: template register/deploy/undeploy/deregister, tag create/set/delete,
instance create/pause/resume/kill/terminate/debug-override, breakpoint
create/delete/resume, node reset, message send, lineage prune, and auth key
create/rotate/revoke.

`asset:delete` was not exercised. Its dry-run path needs a committed,
durable-lifetime claim handle on an instance; the bundled filesystem claim
producer was configured and a template acquired a claim through it with
`lifetime: durable`, and the instance ran to terminal, but the instance's asset
listing stayed empty, so no asset existed to preview a delete of.

RESULT: 22 pass, 0 fail, 1 not exercised
