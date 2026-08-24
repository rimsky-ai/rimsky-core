---
concept: permission
aliases:
  - grant
  - action
---

# Permission

## What it is

A permission is the authorization grant one api-key carries (see `concept:api-key`). The grant is a set of entries. Each entry names an action — what may be done, and to what kind of resource — and may also carry a mode, the floor below which that key's requests cannot execute (see `concept:dry-run`), and a scope, a selector the request's target resource must satisfy. A permission has four parts: the shape of an entry, the matcher that decides whether an entry's action covers a request, the evaluator that decides a request against the whole set, and the registry of the actions a deployment recognizes.

## Purpose

A permission lets an operator say what one key may do, in a vocabulary small enough to check when the key is minted and to evaluate on every request. Because an entry may narrow itself by mode and by target resource, an operator mints a key that reaches exactly the resources one caller needs and executes only as far as that caller should go.

## Boundaries

A permission owns the shape of a grant entry, the matcher over the action vocabulary, the registry of recognized actions, and the per-action scoping the optional selector gives.

It does not own the routing of a request to its handler, the expansion of a named role into a grant, which belongs to `concept:role-template`, or the resolution of a request's mode, which belongs to `concept:dry-run` — a permission owns only the entry's mode field that feeds that resolution. It does not own the certificate machinery the enrollment grant unlocks, which belongs to `concept:service-auth`.

see also: `api-key`, `control-api`, `dry-run`, `role-template`, `service-auth`

## Aliases

- grant
- action
