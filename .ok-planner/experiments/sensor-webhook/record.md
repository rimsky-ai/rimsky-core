---
experiment: sensor-webhook
commit: PENDING
---

# An external caller triggers a node by POSTing to an authenticated route

## What it ran against

A private docker network carrying a `rimsky-all-in-one` orchestrator and a
`rimsky-sensor-webhook` container whose HTTP listener is published to the host,
so the POSTs come from outside the network the orchestrator runs on. The
template declares two message types, one node subscribed to both, and two
publishers of kind `webhook`: one on path prefix `/hooks/secret` authenticated
by a shared header, one on `/hooks/hmac` authenticated by an HMAC signature over
a timestamp and the body with a sixty-second replay window and a delivery-id
header for redelivery. `run.py` builds and removes everything. Re-run unchanged at this tree.

## What was observed

The sensor served its health route before any subscription existed, and both
declared subscriptions mounted live. A POST carrying the shared header returned
200, and the message was already on the target instance when the call
returned — no poll interval sits between the call and the message. The
subscribed node ran on it.

A POST with no header and a POST with the wrong header were each refused 401,
and the instance still held exactly one message from that route. A correctly
signed POST returned 200 and was likewise already a message, carrying the
delivery id. A POST signed with the wrong secret was refused 401, and a
correctly signed POST bearing a timestamp an hour old was refused 401 by the
replay window. Redelivering a body under a delivery id already seen returned 200
and produced no second message. A POST to a path no subscription declared was
refused 404. Every message the instance received came from the sensor.
