---
assessment: sensor-webhook--signature-and-replay-window
subject: story:sensor-webhook
way: signature-and-replay-window
release: d977250c
outcome: held
warrant: experiment:sensor-webhook
---
# A signed call is accepted, a wrongly signed or stale one is not

The audit declared a second endpoint on `catalog:bundled-services/sensor-webhook` authenticated by a signature over a timestamp and the body, with a replay window. A correctly signed call was accepted and was likewise already a message when it returned, carrying its delivery id. A call signed with the wrong secret was refused, and a correctly signed call bearing a timestamp an hour old was refused by the declared window — so a captured body cannot be replayed later even with a valid signature. Both authentication forms the operator can declare were therefore measured, not just the simpler one.

## Unverified remainder

One window length and one signing scheme were exercised. The demonstration does not establish behaviour at the exact window boundary or under clock skew between caller and sensor.
