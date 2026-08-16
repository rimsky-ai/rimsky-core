---
assessment: message-bus--send-with-dedup-key
subject: story:message-bus
way: send-with-dedup-key
release: d977250c
outcome: held
warrant: experiment:message-bus
---
# Sending a message into a live instance's bus, with the dedup key mandatory

The audit sent messages into a live instance through `catalog:http-routes/POST /v1/instances/{id}/messages` on an all-in-one deployment (`catalog:images/rimsky-all-in-one`). The dedup key is genuinely mandatory rather than merely expected: a send that omitted it was refused outright, while a send carrying one was accepted and returned the message's own id. Both sent bodies reached the downstream node that subscribes to the message type, which is the "downstream nodes consume the bus" end of the promise. Thirteen checks across this way and its siblings, none failing.

## Unverified remainder

The send was driven by an operator caller. This way does not establish the behaviour of the same route under concurrent senders racing on one key.
