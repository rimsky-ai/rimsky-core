---
assumption: key-expiry-emits-an-event
commit: PENDING
disposition: trap
synthesized: 2026-08-16T05:48:16Z
---

# a key lapsing at its expiry produces an audit event the way creation, rotation, and revocation do, so expiry is visible in the audit feed.

As operator monitoring credentials, I would take it that a key lapsing at its expiry produces an audit event the way creation, rotation, and revocation do, so expiry is visible in the audit feed.

## Source

sibling-symmetry — `auth.key_created`, `auth.key_revoked`, `auth.key_rotated` with no expiry kind

## What a run would observe

mint a short-expiry key, let it lapse, and query `GET /v1/audit` for a row describing the lapse.

## Measured

Experiment `assumption-key-expiry-emits-an-event`, run at this tree against one
`rimsky-all-in-one` container. The three sibling phases do emit: a revoked key
carries `auth.key_created, auth.key_revoked` and a rotated one carries
`auth.key_created, auth.key_rotated`. A lapse emits nothing. Two keys were minted
sharing a five-second expiry; one was used until its request turned 401, and
after that its audit rows still carried only `auth.key_created` as a lifecycle
kind, while the key that lapsed at the same moment without being used has exactly
one audit row, its creation. `kind=auth.key_expired`, `kind=auth.key_lapsed` and
`kind=auth.key_expiry` are each refused 400 — the audit surface carries five
kinds (`auth.access_attempted`, `auth.access_denied`, `auth.key_created`,
`auth.key_revoked`, `auth.key_rotated`) and none is an expiry. What the operator
gets instead is reactive and partial: the lapsed key's refused request is
recorded as `auth.access_denied` with `denial_reason: "expired_token"`, so the
lapse surfaces only when some client trips over the dead key, and never at all
for a key nobody uses again. The row itself stays listed by `rimsky auth list`
with its `expires_at`, and `GET /v1/auth/status` counts it out of the active
total, so the state is inspectable on demand — it is the feed that is silent.
