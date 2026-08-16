---
assessment: asset-management--version-history
subject: story:asset-management
way: version-history
release: d977250c
outcome: held
warrant: experiment:asset-management
---
# Walking an asset's version history

`catalog:cli-verbs/rimsky asset versions` returned the version the producer had minted, with its commit time and the producer's own metadata alongside it. The history is the producer's record surfaced through the asset verb, not a shadow copy rimsky keeps: what comes back is whatever the producer behind the asset holds. An operator therefore reads one history whether they ask rimsky or the store.

## Unverified remainder

The run's producer had minted a single version, so the way was demonstrated on a one-entry history rather than on a sequence of successive versions.
