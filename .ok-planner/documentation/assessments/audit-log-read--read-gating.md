---
assessment: audit-log-read--read-gating
subject: story:audit-log-read
way: read-gating
release: d977250c
outcome: held
warrant: experiment:audit-log-read
---
# The audit is itself a guarded read

Reading the audit is gated like any other action on the deployment. A key with the `catalog:bundled-roles/read-only` role was admitted to `catalog:http-routes/GET /v1/audit`, and a key that does not carry the audit-read action was refused. The log that records who did what is therefore not a way around the permissions it records.

## Unverified remainder

None: the passing run demonstrates the way as promised.
