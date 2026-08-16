---
trap: key-expiry-emits-an-event
release: d977250c
---
# Evidence set — a key lapsing at its expiry produces an audit event the way creation, rotation, and revocation do, so expiry is visible in the audit feed.

Source of the prior: sibling-symmetry — `auth.key_created`, `auth.key_revoked`, `auth.key_rotated` with no expiry kind

## What the audit ran and observed (assumption record)

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

## Experiment record (experiment:assumption-key-expiry-emits-an-event)

# Does a key lapsing at its expiry write an audit event?

## What it ran against

One `rimsky-all-in-one` container from the tree's own image tag, bootstrapped
with `rimsky auth init`. It first exercises the three lifecycle phases that do
emit — create, revoke, rotate — then mints two keys sharing a five-second expiry,
uses one and leaves the other untouched, polls the used key until it stops being
accepted, and reads `GET /v1/audit` filtered by key name for both.

## What was observed

Revoke and rotate each write their kind: `revoked-one` carries
`auth.key_created, auth.key_revoked` and `rotated-one` carries
`auth.key_created, auth.key_rotated`.

A lapse writes nothing. After the used key turned 401, its audit rows carried no
new `auth.key_*` kind — still only `auth.key_created`. The key that lapsed at the
same moment without being used has exactly one audit row, its creation. Three
candidate filters, `kind=auth.key_expired`, `kind=auth.key_lapsed` and
`kind=auth.key_expiry`, are each refused 400; the audit surface carries five
kinds — `auth.access_attempted`, `auth.access_denied`, `auth.key_created`,
`auth.key_revoked`, `auth.key_rotated` — and none is an expiry.

What an operator sees instead is reactive. The lapsed key's refused request is
recorded as `auth.access_denied` with `denial_reason: "expired_token"`, so the
lapse becomes visible the moment a client trips over it and not before. The
lapsed row stays listed by `rimsky auth list` with its `expires_at` on it, and
`GET /v1/auth/status` counts it out of the active total.

EXPERIMENT PASS (15 checks)

Runnables: `src:.ok-planner/experiments/assumption-key-expiry-emits-an-event/` at the stamped commit.
