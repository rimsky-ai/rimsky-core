---
audit: dry-run-request-flag
artifact: story:dry-run-request-flag
determination: unclear
compliance: compliant
commit: PENDING
audited: 2026-08-10T05:25:00Z
checked: 23
unaccounted: 1
---

# Every write submitted with the per-request dry-run flag

Unclear, because one member of the population could not be measured. The
population is the write half of the control API's action registry, 23 actions.
For 22 of them the flagged request returned a synthetic envelope naming exactly
one would-have intent, left the state that write would have changed unchanged on
re-read, and was then accepted live as a control — covering template
register, deploy, undeploy and deregister; tag create, set and delete; instance
create, pause, resume, kill, delete and debug-override; breakpoint create, delete
and resume; node reset; message send; lineage prune; and api-key create, rotate
and revoke. The 23rd, asset delete, needs a committed durable-lifetime claim
handle to preview a delete of; the bundled filesystem claim producer was
configured and a template acquired a claim through it with durable lifetime, but
the instance's asset listing stayed empty after the run reached terminal, so the
precondition never existed and no run was taken.

## Unaccounted

- `asset:delete` — no run taken; the deployment produced no asset to preview a delete of.
