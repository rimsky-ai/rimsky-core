---
assessment: held-commit-cascades-success--success-at-commit
subject: story:held-commit-cascades-success
way: success-at-commit
release: d977250c
outcome: held
warrant: experiment:held-commit-cascades-success
---
# Relying on an upstream's success reaching a subscriber only once its held work commits

A template declared an acquirer opening a claim on the bundled `catalog:bundled-services/claim-producer-filesystem`, a co-holder calling an endpoint that holds every request open until released, and a watcher outside the holding subgraph subscribed to the acquirer's success. The endpoint's report that the co-holder's request had arrived is the synchronisation point, so the provisional moment is observed rather than guessed at. At that moment the acquirer's run was held, it had emitted no success signal, and the watcher had no run at all. After the release the claim resolved with a single `catalog:event-kinds/claim_resolution.commit`, the acquirer emitted exactly one success at the next sequence number after that commit, and the watcher's `catalog:event-kinds/work_started` followed it. A downstream node therefore acts on held results only once they can no longer be rolled back. Seven checks, none failing.

## Unverified remainder

None: the passing run demonstrates the way as promised.
