---
experiment: assumption-permission-wildcards-are-globs
commit: PENDING
---

# Does the permission wildcard behave like a glob?

## What it ran against

One `rimsky-all-in-one` container from the tree's own image tag, bootstrapped
with `rimsky auth init`. It submits eleven glob-shaped action strings to
`POST /v1/auth/keys` and records the response, mints the three shapes that are
accepted and drives each minted key against real gated routes, then probes the
match boundary and the registry check.

## What was observed

Eleven glob shapes were refused at key creation, every one with HTTP 400:
`instance:re*`, `instance:*ead`, `instance:re*d`, `inst*:read`, `*ance:read`,
`*:re*`, `instance:**`, `*:*`, `**`, `instance:read*`, `*instance:read`. The
error names the whole vocabulary — for `instance:re*` it is exactly
`entry 0: action "instance:re*": unsupported wildcard shape (only '*',
'<noun>:*', '*:<verb>' allowed)`.

The three literal forms mint (201) and work as named. A `template:*` key reads
the template list (200) and registers a template (201) and is refused
`instance:read` and `tag:read` (403 each). A `*:read` key reads instances and the
audit log (200) and is refused `instance:create` (403).

The separator is part of the match, not a substring boundary: a `nod:*` key does
not reach `node:read` (403), and a `*:ead` key does not reach `instance:read`
(403).

The registry check is applied only to literal actions. `banana:read` is refused
with `unknown action: banana:read`, while `banana:*`, `backfill:*`,
`*:frobnicate` and `instanc:*` each mint 201 and then reach nothing (403) — a
mistyped noun in a wildcard grant produces a key that authenticates and
authorizes nothing, with no error at mint.

EXPERIMENT PASS (29 checks)
