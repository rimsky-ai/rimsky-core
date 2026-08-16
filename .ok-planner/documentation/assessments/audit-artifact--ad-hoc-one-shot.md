---
assessment: audit-artifact--ad-hoc-one-shot
subject: story:audit-artifact
way: ad-hoc-one-shot
release: d977250c
outcome: held
warrant: experiment:audit-artifact
---
# Inspecting the record a self-hosted one-shot run left behind

`catalog:cli-verbs/rimsky run` self-hosted an ad-hoc template with the same mixed roster and behaved the same way: it exited non-zero for the mixed outcome and left its own artifact directory holding the run's whole record. Served back through an ordinary deployment, the record carried the instance terminal, replayed both legs' terminals including the failure's own error class, and made the succeeding leg's attribute writeback readable. Both one-shot modes the product offers therefore leave a record an operator can debug and verify from, so the choice between them does not cost the audit trail.

## Unverified remainder

None: the passing run demonstrates the way as promised.
