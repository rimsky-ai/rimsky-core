---
assessment: dry-run-request-flag--per-request-preview
subject: story:dry-run-request-flag
way: per-request-preview
release: d977250c
outcome: held
warrant: experiment:dry-run-request-flag
---
# Asking for any single write to be previewed rather than performed

The write half of the control API's action registry is 23 actions, and all 23 were driven against a fresh deployment with authentication enabled. Each was submitted as its real request with the preview asked for, had to come back naming exactly one thing it would have done, had the state it would have changed re-read and required unchanged, and was then submitted live, so an envelope obtained by failing validation could not pass for a preview. All 23 passed: template register, deploy, undeploy and deregister; tag create, move and delete; instance create, pause, resume, kill, terminate and debug-override; breakpoint create, delete and resume; node reset; message send; lineage prune; key create, rotate and revoke; and `catalog:permission-actions/asset:delete`. The last needed a subject that a default deployment does not have, so the run stood up a claim producer advertising the data-processing protocol and materialized a durable claim through it before previewing the deletion. An operator can preview any of these before committing to it, and sees the same validation the live write would apply.

## Unverified remainder

None: the passing run demonstrates the way as promised, over the whole population of write actions with none unaccounted for.
