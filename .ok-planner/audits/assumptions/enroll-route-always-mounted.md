---
assumption: enroll-route-always-mounted
commit: PENDING
disposition: trap
synthesized: 2026-08-16T05:48:16Z
---

# `POST /v1/enroll` and `GET /v1/ca-root` are always available, so a service can enroll regardless of the deployment's `peer_auth` setting.

As service author testing enrollment, I would take it that `POST /v1/enroll` and `GET /v1/ca-root` are always available, so a service can enroll regardless of the deployment's `peer_auth` setting.

## Source

sibling-symmetry — both routes listed unconditionally in the public route set beside `peer_auth` as an optional config key

## What a run would observe

call `/v1/enroll` and `/v1/ca-root` against a stack running `peer_auth: none` and record the statuses.

## Measured

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
