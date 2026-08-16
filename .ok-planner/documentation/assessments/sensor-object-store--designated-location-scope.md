---
assessment: sensor-object-store--designated-location-scope
subject: story:sensor-object-store
way: designated-location-scope
release: d977250c
outcome: held
warrant: experiment:sensor-object-store
---
# Only content inside the designated location becomes work

The audit deposited a file outside the designated prefix and it was never handed over, while a file deposited under the prefix afterwards was. That ordering is what shows the sensor kept listing the location rather than reading it once at startup: the later arrival still became a message. The operator's designation therefore bounds what enters the graph, and neighbouring content in the same store stays out of it.

## Unverified remainder

One prefix on one bucket was exercised. The demonstration does not establish behaviour when the designated location itself disappears or when two publishers designate overlapping prefixes.
