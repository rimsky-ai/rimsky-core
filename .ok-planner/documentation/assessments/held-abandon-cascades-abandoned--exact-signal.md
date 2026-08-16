---
assessment: held-abandon-cascades-abandoned--exact-signal
subject: story:held-abandon-cascades-abandoned
way: exact-signal
release: d977250c
outcome: held
warrant: experiment:held-abandon-cascades-abandoned
---
# Subscribing to the abandoned-error signal by name

A template declared an acquirer holding a claim on the bundled `catalog:bundled-services/claim-producer-filesystem`, a co-holder whose work fails, and subscribers outside the holding subgraph. The failure rolled the claim back with a single `catalog:event-kinds/claim_resolution.abandon`, and the acquirer emitted exactly one terminal signal, the abandoned error. The subscriber naming that signal exactly ran, its `catalog:event-kinds/work_started` at a sequence number after the abandon, so it learned of the rollback at the moment the held work was abandoned rather than later or not at all. The subscriber on success never ran, so a rollback is never reported downstream as a success. Seven checks across the whole story, none failing.

## Unverified remainder

None: the passing run demonstrates the way as promised.
