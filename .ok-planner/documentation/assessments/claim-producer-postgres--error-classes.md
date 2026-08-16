---
assessment: claim-producer-postgres--error-classes
subject: story:claim-producer-postgres
way: error-classes
release: d977250c
outcome: held
warrant: experiment:claim-producer-postgres
---
# Subscribing to the failure modes the producer declares

The producer advertises exactly three error classes — claim-unavailable, swap-failed, and not-atomically-replaceable — read off the public producer view and counted. Two of the three were driven to fire and to drive a subscriber. A drained pick policy settled its claiming node on claim-unavailable. A canonical schema carrying an external dependent settled its claiming node on not-atomically-replaceable, and the schema and its dependent were left untouched, so the refusal is a refusal rather than a partial change. In both cases a node subscribed to the producer's class namespace ran on the signal.

## Unverified remainder

Swap-failed is advertised but was not provoked at this release: no route through the public surface reached it. An external dependent present before the claim opens is refused earlier, on the not-atomically-replaceable class, and one created by a co-holding node while the claim was open did not make the swap fail.
