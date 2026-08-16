---
audit: dry-run-request-flag
artifact: story:dry-run-request-flag
text: noncompliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:52:00Z
checked: 23
unaccounted: 0
---

# Every write the control API exposes can be previewed per request

Supported, with the whole population covered. The write half of the control
API's action registry is 23 actions, and all 23 were driven against a fresh
deployment: each was submitted as its real request with the per-request preview
asked for, had to come back naming exactly one thing it would have done, had the
state it would have changed re-read and required unchanged, and was then
submitted live so that a preview obtained by failing validation could not pass
for one. All 23 passed — template register, deploy, undeploy and deregister; tag
create, move and delete; instance create, pause, resume, kill, terminate and
debug-override; breakpoint create, delete and resume; node reset; message send;
lineage prune; key create, rotate and revoke; and asset delete. The last needed
a subject that does not exist on a default deployment, so the run stood up a
claim producer advertising the data-processing protocol and materialized a
durable claim through it before previewing the delete.

## Compliance

The body prescribes mechanism: "a per-request dry-run flag" names the request shape and "a synthetic envelope" names the response shape, both of which belong to a decision; compliant text would say the operator can ask for any write to be previewed rather than performed and see what it would have done, validated the same way as the real write and changing nothing.
