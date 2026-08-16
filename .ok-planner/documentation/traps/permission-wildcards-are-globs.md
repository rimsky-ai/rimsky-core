---
trap: permission-wildcards-are-globs
release: d977250c
demonstration: experiment:assumption-permission-wildcards-are-globs
---
## Assumption

As operator writing a grant, I would take it that the wildcard vocabulary behaves like ordinary globs, so `instance:re*` matches `instance:read` and `instance:resume`, and `template:*` and `*:read` also work.

craft-convention — `*` in an authorization grammar reading as a glob

## Actual behavior

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
