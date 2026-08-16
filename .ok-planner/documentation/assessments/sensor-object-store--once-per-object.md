---
assessment: sensor-object-store--once-per-object
subject: story:sensor-object-store
way: once-per-object
release: d977250c
outcome: held
warrant: experiment:sensor-object-store
---
# Each deposited object is handed to the graph exactly once

The audit deposited three objects under the designated prefix over the life of the run and read the instance's messages back through `catalog:http-routes/GET /v1/instances/{id}/messages`. The graph saw three messages for three objects, none handed over twice, and the subscribed node ran three times. Each deposit drove its own run rather than being folded into a batch, so a producer dropping several objects gets several units of work and no duplicates.

## Unverified remainder

Three objects deposited in sequence were exercised. The demonstration does not establish the once-only guarantee across a restart of the sensor, nor for objects deposited faster than the poll interval.
