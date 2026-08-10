---
experiment: anonymous-mode-bootstrap
commit: PENDING
---

# Fresh deployment opens, then locks down

## What it ran against

Two `rimsky-all-in-one` containers from the tree's own image tag: MAIN on the
zero-config default, and MTLS on the same image with `peer_auth: mtls`, which is
what mounts the enrollment route and the CA-root route. The operator surface it
drives is the `rimsky auth init` verb and the ruled control-API routes, reached
with curl. Route coverage is taken from the surface ruling's `http-routes` kind:
82 of the 88 ruled routes belong to the control API, and `routes.tsv` maps each
one to a concrete request. The six not swept are the supervisor callback
listener's three routes, the sensor-webhook's two, and the observability
wildcard, whose concrete members are listed separately in the same file.

## What was observed

Anonymous mode is open: with no token, 80 of the 80 MAIN-swept routes answered
without an authorization refusal — 31 with 2xx, 12 with 400 for the deliberately
empty bodies, 37 with 404 for the deliberately non-existent identifiers — and a
real operator lifecycle (register a template, deploy it, create an instance, read
it, terminate it) ran end to end unauthenticated. On the MTLS stack the CA root
answered 200 unauthenticated and `POST /v1/enroll` was the single refusal, 403
with "enrollment requires an authenticated api-key principal".

`rimsky auth init` minted the first admin key and printed its plaintext once.
Anonymous mode then closed: re-sweeping the same 80 routes with no token returned
401 on 79 of them, the sole exception being `GET /v1/health`, the liveness probe.
The minted key restored access — auth status reported `authenticated`, instance
reads and template registration succeeded, and on the MTLS stack the same
enrollment route that had refused the anonymous caller answered 200 for the key.
