---
assessment: held-abandon-cascades-abandoned--error-family-pattern
subject: story:held-abandon-cascades-abandoned
way: error-family-pattern
release: d977250c
outcome: held
warrant: experiment:held-abandon-cascades-abandoned
---
# Subscribing through the broader error-family pattern instead

The second subscriber on the same acquirer used the error-family pattern rather than naming the abandoned signal, and it ran on the same rollback, its `catalog:event-kinds/work_started` at a sequence number after the `catalog:event-kinds/claim_resolution.abandon`. Both routes therefore deliver the same rollback at the same moment, so a template author who already catches every error of an upstream does not need a separate subscription to learn that held work was rolled back. The success subscriber stayed silent under both.

## Unverified remainder

None: the passing run demonstrates the way as promised.
