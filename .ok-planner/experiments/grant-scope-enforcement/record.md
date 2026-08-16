---
experiment: grant-scope-enforcement
commit: d977250c
---

# Least-privilege delegation across the template lifecycle

## What it ran against

One `rimsky-all-in-one` container from the tree's own image tag. An admin key
minted by `rimsky auth init` delegates to a per-tenant key created with `rimsky
auth create-key --role-file`, whose grant scopes seven actions to the template
tag "alpha" and grants unscoped reads. Every scopeable action is then driven
twice from the tenant key: once at the in-scope tag, once at a "beta" tag the
admin owns. Re-run unchanged at this tree.

## What was observed

The scoped grant read back off the key with all seven entries carrying the
"alpha" scope. Across the whole template lifecycle each in-scope call succeeded
and each out-of-scope call was refused with 403: register (201 / 403), deploy
(200 / 403), tag move (200 / 403), tag delete (200 / 403), instance create by tag
(created / 403), instance create by the template's own hash (created for alpha,
403 for beta — the hash resolves through the tag rows, so naming the resource by
id does not evade the scope), undeploy (200 / 403) and deregister (200 / 403).
The beta template survived every out-of-scope attempt and still read 200 for the
admin. The tenant key remained a real key for what it was granted (template list
200) and was refused what it was not (mint a key, 403).

EXPERIMENT PASS
