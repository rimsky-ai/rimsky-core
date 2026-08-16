---
assessment: sensor-webhook--shared-secret-auth
subject: story:sensor-webhook
way: shared-secret-auth
release: d977250c
outcome: held
warrant: experiment:sensor-webhook
---
# A shared credential gates the endpoint, and an unauthenticated call becomes nothing

The audit called the declared path of `catalog:bundled-services/sensor-webhook` three ways: with the right credential, with no credential, and with the wrong one. Only the correct call was accepted; the other two were refused, and neither became a message on the target instance. Refusal is therefore complete rather than partial — a caller without the operator's credential cannot put work into the graph, and the instance's message count is unchanged by the attempt.

## Unverified remainder

One shared-credential form was exercised. The demonstration does not establish rotation of that credential while the sensor is running.
