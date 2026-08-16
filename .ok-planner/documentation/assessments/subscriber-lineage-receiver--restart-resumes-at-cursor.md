---
assessment: subscriber-lineage-receiver--restart-resumes-at-cursor
subject: story:subscriber-lineage-receiver
way: restart-resumes-at-cursor
release: d977250c
outcome: held
warrant: experiment:subscriber-lineage-receiver
---
# Restarting the subscriber resumes rather than replaying

The audit restarted the subscriber and ran a second workflow. That added four more deliveries, eight distinct in all, with nothing redelivered from the first workflow — so the restarted subscriber resumed at its cursor rather than replaying history into the governance platform. An operator can restart the subscriber without polluting the receiver with duplicates.

## Unverified remainder

One restart between two workflows was exercised. The demonstration does not establish behaviour when the subscriber is stopped mid-delivery, nor whether a delivery in flight at that moment is repeated.
