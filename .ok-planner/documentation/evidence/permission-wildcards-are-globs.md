---
trap: permission-wildcards-are-globs
release: d977250c
---
# Evidence set — the wildcard vocabulary behaves like ordinary globs, so `instance:re*` matches `instance:read` and `instance:resume`, and `template:*` and `*:read` also work.

Source of the prior: craft-convention — `*` in an authorization grammar reading as a glob

## What the audit ran and observed (assumption record)

Experiment `assumption-permission-wildcards-are-globs`, re-run at this tree
against one `rimsky-all-in-one` container. The infix wildcard the prior names is
refused at key creation: `POST /v1/auth/keys` with `{"action": "instance:re*"}`
answers 400 with `entry 0: action "instance:re*": unsupported wildcard shape
(only '*', '<noun>:*', '*:<verb>' allowed)`. Ten further glob shapes are refused
the same way — `instance:*ead`, `instance:re*d`, `inst*:read`, `*ance:read`,
`*:re*`, `instance:**`, `*:*`, `**`, `instance:read*`, `*instance:read`. The two
shapes the prior also names do work: `template:*` mints and grants
`template:read` and `template:register` while refusing `instance:read` and
`tag:read`, and `*:read` mints and grants `instance:read` and `audit:read` while
refusing `instance:create`. The separator is part of the match, so `nod:*` does
not reach `node:read` and `*:ead` does not reach `instance:read`. One further
observation: the registry check skips wildcards, so `banana:*`, `backfill:*`,
`*:frobnicate` and `instanc:*` each mint 201 and then authorize nothing, while
the literal `banana:read` is refused with `unknown action: banana:read`. An
operator who writes a glob gets a loud 400; an operator who mistypes a noun
inside a valid wildcard shape gets a silent key that grants nothing.

## Experiment record (experiment:assumption-permission-wildcards-are-globs)

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

Runnables: `src:.ok-planner/experiments/assumption-permission-wildcards-are-globs/` at the stamped commit.
