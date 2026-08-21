---
decision: held-claim-poison-propagation
---

# A held claim abandons at the first holder failure

## Choice

A held claim's aggregate outcome resolves to Abandon the moment any holder fails, not when the last holder settles. Rimsky marks every still-active holder's co-holder row failed at that moment. A holder still in flight settles as failed under the abandoned error class whatever its executor returns (see `concept:claim-handle`, `concept:auto-terminal`).

## Rationale

A held claim is one coordinated unit spanning one claim's lifetime. Once part of that unit has failed, the claim abandons whatever the rest produce, so further work runs against an outcome already decided. Resolving at the first failure releases the claim sooner and gives every holder the same outcome: the unit failed, so each part settles as failed for cascade purposes. A holder whose executor succeeded after the resolution still settles failed, because its output belongs to a unit rimsky abandoned.

## Alternatives

- Wait for every holder to settle before deciding the outcome — rejected: the verdict is already fixed at the first failure, so the wait holds the claim open and lets holders whose outcome is already fixed keep running.
- Let a late-succeeding holder keep its success terminal — rejected: subscribers outside the holding subgraph would see success from a member of a unit that abandoned.
