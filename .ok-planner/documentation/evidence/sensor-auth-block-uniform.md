---
trap: sensor-auth-block-uniform
release: d977250c
---
# Evidence set — the `auth` block that the webhook publisher takes (`hmac`, `secret_header`, `none`) is available on every publisher kind, so an HTTP-poll sensor can present credentials to its upstream the same way.

Source of the prior: sibling-symmetry — `webhook: auth.*` keys with no `http: auth.*` counterpart in `publisher-config-keys`

## What the audit ran and observed (assumption record)

Experiment `assumption-sensor-auth-block-uniform` (nine checks, none failing)
declared the same `auth: {mode: secret_header, header: X-Probe-Token, secret: …}`
block on an `http` publisher and on a `webhook` publisher in one template, drove
a `rimsky-all-in-one` stack with the two sensor images at this tree's tag, and
read the poll request off a recording sink. The prior does not hold, in the
worse direction: registration accepts the block on the `http` publisher (201),
deploy accepts it (200) and the subscription mounts live — and then the sensor
polls the upstream with `accept-encoding`, `host` and `user-agent` and nothing
else. No `X-Probe-Token`, no `Authorization`, the secret nowhere in the request,
and the poll still produced its message. The block is decoded into a struct that
has no such field and dropped in silence.

The same block on the `webhook` publisher is enforced: a delivery with no
credential was refused 401 and the same delivery carrying the header was
accepted 200; and the webhook publisher that declared no `auth` block never
mounted, the control API's log carrying the sensor's refusal `resolved_config
.auth required (set mode to hmac, secret_header, or none)` on retry after retry.

So one publisher kind requires the block and enforces it, another accepts it and
ignores it. An operator who writes the block on an HTTP-poll publisher believing
the sensor presents those credentials upstream gets an unauthenticated poll and
no warning anywhere — at registration, at deploy, at subscribe, or in the
sensor's log.

## Experiment record (experiment:assumption-sensor-auth-block-uniform)

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

Runnables: `src:.ok-planner/experiments/assumption-sensor-auth-block-uniform/` at the stamped commit.
