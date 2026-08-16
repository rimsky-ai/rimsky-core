---
trap: enroll-route-always-mounted
release: d977250c
---
# Evidence set — `POST /v1/enroll` and `GET /v1/ca-root` are always available, so a service can enroll regardless of the deployment's `peer_auth` setting.

Source of the prior: sibling-symmetry — both routes listed unconditionally in the public route set beside `peer_auth` as an optional config key

## What the audit ran and observed (assumption record)

Experiment `assumption-enroll-route-always-mounted`, run at this tree across
three `rimsky-all-in-one` containers. Both routes are conditional on a deployment
CA. On the zero-config default (no `peer_auth` key) `POST /v1/enroll` and
`GET /v1/ca-root` answer 404 unauthenticated, with an admin key, and with a key
whose grant is exactly `service:enroll`; the body is chi's `404 page not found`,
so the router has no such path rather than a handler refusing the caller, and the
same stack answers its other routes normally and mints the `service:enroll` key
without complaint. Writing `peer_auth: none` explicitly behaves identically. With
`peer_auth: mtls` both routes appear and the control API itself turns HTTPS:
`GET /v1/ca-root` answers 200 unauthenticated with a PEM, `POST /v1/enroll`
unauthenticated is refused by the handler (403,
`enrollment requires an authenticated api-key principal`), and with a
`service:enroll` key it answers 200 returning `cert_pem`, `key_pem`,
`ca_root_pem` and `not_after`. A service author testing enrollment against a
default deployment sees 404s that read like a missing feature rather than a
configuration the deployment has not turned on.

## Experiment record (experiment:assumption-enroll-route-always-mounted)

# Are the enrollment routes available regardless of peer_auth?

## What it ran against

Three `rimsky-all-in-one` containers from the tree's own image tag, each on its
own free port with its own mounted `rimsky.yml`: one on the zero-config default
(no `peer_auth` key), one with `peer_auth: none` written out, one with
`peer_auth: mtls` and a CA encryption key. Each is driven at `POST /v1/enroll`
and `GET /v1/ca-root` unauthenticated, with an admin key, and with a key carrying
the `service:enroll` grant.

## What was observed

Without `peer_auth`, neither route exists. `POST /v1/enroll` and
`GET /v1/ca-root` answer 404 with an admin key, unauthenticated, and with a key
whose grant is exactly `service:enroll`. The body is chi's `404 page not found`,
so it is the router missing the path rather than a handler refusing the caller,
while the same stack answers its other routes normally and mints the
`service:enroll` key without complaint. Writing `peer_auth: none` explicitly
behaves identically: 404 on both.

With `peer_auth: mtls` both routes appear, and the control API itself turns
HTTPS. `GET /v1/ca-root` answers 200 unauthenticated with a PEM certificate.
`POST /v1/enroll` unauthenticated is refused by the handler, not the router — 403
with `enrollment requires an authenticated api-key principal`. With a
`service:enroll` key it answers 200 and returns `cert_pem`, `key_pem`,
`ca_root_pem` and `not_after`.

A service author testing enrollment against a default stack sees 404s that read
like a missing feature; the routes are conditional on a deployment CA existing.

EXPERIMENT PASS (13 checks)

Runnables: `src:.ok-planner/experiments/assumption-enroll-route-always-mounted/` at the stamped commit.
