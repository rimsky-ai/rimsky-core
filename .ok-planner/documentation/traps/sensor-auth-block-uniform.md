---
trap: sensor-auth-block-uniform
release: d977250c
demonstration: experiment:assumption-sensor-auth-block-uniform
---
## Assumption

As operator exposing a sensor, I would take it that the `auth` block that the webhook publisher takes (`hmac`, `secret_header`, `none`) is available on every publisher kind, so an HTTP-poll sensor can present credentials to its upstream the same way.

sibling-symmetry — `webhook: auth.*` keys with no `http: auth.*` counterpart in `publisher-config-keys`

## Actual behavior

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
