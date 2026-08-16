---
experiment: assumption-sensor-auth-block-uniform
commit: PENDING
---

# The same auth block on two publisher kinds

## What it ran against

A private docker network carrying a `rimsky-all-in-one` orchestrator with
`rimsky.yml` wiring three publishers, a `rimsky-sensor-http` container with the
private range allowlisted so it can reach the run's own upstream, a
`rimsky-sensor-webhook` container with its ingress published, and a recording
HTTP sink (`sink.py` under `python:3.12-alpine`) that answers every request and
logs the headers it was given.

One template declares three publishers: an `http` poll publisher carrying
`auth: {mode: secret_header, header: X-Probe-Token, secret: …}`, a `webhook`
publisher carrying the identical block, and a `webhook` publisher carrying no
`auth` block at all.

## What was observed

Nine checks, none failing.

The `auth` block on the `http` publisher is accepted everywhere and applied
nowhere. Registration returned 201, deploy returned 200, and the poll
subscription mounted live. The sink then recorded the poll: a `GET` carrying
`accept-encoding`, `host` and `user-agent` and nothing else — no
`X-Probe-Token`, no `Authorization`, and the secret nowhere in the request. The
poll still produced its message, so the declaration was dropped rather than
enforced or refused.

The identical block on the `webhook` publisher is load-bearing. A delivery to
its path with no credential was refused 401; the same delivery carrying
`X-Probe-Token` was accepted 200. And the webhook publisher that declared no
`auth` block never mounted at all: the control API's log carries the sensor's
refusal, `resolved_config.auth required (set mode to hmac, secret_header, or
none)`, retried and never satisfied.
