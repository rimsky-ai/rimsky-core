---
assessment: audit-log-read--record-kinds
subject: story:audit-log-read
way: record-kinds
release: d977250c
outcome: held
warrant: experiment:audit-log-read
---
# Every auth-relevant action shows up in the audit, attributable

The audit provoked each action the story names against a fresh deployment of `catalog:images/rimsky-all-in-one` and read them back through `catalog:http-routes/GET /v1/audit`. All five record kinds were present and counted: four `catalog:event-kinds/auth.key_created`, one `catalog:event-kinds/auth.key_revoked`, one `catalog:event-kinds/auth.key_rotated`, nine `catalog:event-kinds/auth.access_attempted` and three `catalog:event-kinds/auth.access_denied`. Each record carried what an operator needs to attribute it: the minted keys by name, the revoked and rotated keys by name, a dry-run write recorded in dry-run mode and marked not executed against the real write's execute mode and executed, and the three denials distinguished by their reasons — invalid token, no token, and insufficient permission. Reading the log therefore answers who did what and when, and separates a refusal for a bad credential from a refusal for a credential that was simply not allowed to do that.

## Unverified remainder

None: the passing run demonstrates the way as promised across all five record kinds the story enumerates.
