---
assessment: frame-origin-audit--publisher-message
subject: story:frame-origin-audit
way: publisher-message
release: d977250c
outcome: held
warrant: experiment:frame-origin-audit
---
# Seeing that a frame opened because a publisher delivered a message

An external POST reached a `catalog:images/rimsky-sensor-webhook` container paired with the orchestrator on one network, arriving as a publisher message. The frame it opened named its triggering message id, type, sender and a sender kind reading as the publisher, and reading that frame on its own returned the same trigger the frame list gave. The triggering message id resolved through `catalog:http-routes/GET /v1/messages/{id}` to a message whose type and sender kind matched the frame's report. An operator asking why the frame opened is therefore answered directly, without correlating an external system's logs with the instance.

## Unverified remainder

None: the passing run demonstrates the way as promised.
