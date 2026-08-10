---
experiment: api-key-management
commit: PENDING
---

# Operator administers the api-key lifecycle

## What it ran against

One `rimsky-all-in-one` container from the tree's own image tag, driven entirely
through the `rimsky auth` CLI verbs — `init`, `create-key`, `list`, `show`,
`revoke`, `rotate`, `status` — with each effect checked independently against the
control API using the key in question.

## What was observed

On a fresh deployment `auth status` reported anonymous with zero keys; `auth
init` minted the admin key and printed its plaintext once, after which `auth
status` reported authenticated with one key and one admin, and a second `auth
init` refused with exit 1.

`auth create-key --role read-only` minted a key that could read instances (200)
and could not register a template (403), so the role bound. `--expires 24h`
minted a working key.

`auth list --json` named all three keys and carried no field matching
"plaintext"; neither its output nor `auth show`'s contained the live plaintext of
the key being inspected. `auth show` reported the key's name and its grant.

`auth revoke` made the revoked key's next request 401, dropped it from the
default listing and kept it visible under `--include-revoked`.

`auth rotate --grace 5s` printed a new plaintext and the old key's revoke time.
The new key worked immediately and the old key still worked inside the window;
polling until the old key stopped being accepted, it turned 401 while the new key
kept answering 200. Final `auth status`: authenticated, 3 keys, 1 admin.
