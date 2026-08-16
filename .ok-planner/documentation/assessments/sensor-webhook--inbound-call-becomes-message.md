---
assessment: sensor-webhook--inbound-call-becomes-message
subject: story:sensor-webhook
way: inbound-call-becomes-message
release: d977250c
outcome: held
warrant: experiment:sensor-webhook
---
# An external caller triggers a node by calling the endpoint, with no poll in between

The audit ran `catalog:bundled-services/sensor-webhook` with its listener published outside the network the orchestrator runs on, so the calls arrive the way an external system's would. The sensor answered its health route before any subscription existed, and both declared subscriptions mounted live on the instance. An authenticated call on the declared path returned success and was already a message on the target instance when the call returned — no poll interval sits between the call and the message — and the subscribed node ran on it. A call to a path no subscription declared was refused, so the declared subscription is what opens a route. Every message the instance held came from the sensor.

## Unverified remainder

One call at a time on two declared paths was exercised. The demonstration does not establish the sensor's behaviour under concurrent calls or a body larger than the run's.
