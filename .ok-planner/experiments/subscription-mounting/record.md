---
experiment: subscription-mounting
commit: d977250c
---

# Watching a publisher subscription mount

## What it runs against

`run.py` creates a docker network, starts `rimsky-sensor-cron` on it at
`RIMSKY_IMAGE_TAG`, and boots a `rimsky-all-in-one` container on the same
network with a mounted `rimsky.yml` registering that sensor as the publisher
`tick`. The template declares one message type, one node reacting to it, and one
publisher entry firing every minute. The run polls `GET /v1/instances/{id}` for
the per-subscription state, then waits for a publisher-sent message. It then
deploys a second template whose publisher entry names a publisher this
deployment does not run.

## What was observed

Eight checks, none failing. The instance exposes one subscription per declared
publisher entry, carrying the publisher name, its kind and its message type. The
operator saw that subscription in the `mounting` state and then in the `active`
state, and in no other state. A `tick/minute` message attributed to the
publisher `tick` then arrived and the node the template wired to that type ran,
so `active` names a sensor that is feeding the instance.

The second instance was created successfully — HTTP 201 with an instance id —
while its subscription reports `failed` with the reason that the publisher is
not registered in this deployment's publisher registry. The create response
carried no sign of that.
