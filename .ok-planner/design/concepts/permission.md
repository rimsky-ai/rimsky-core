---
concept: permission
status: as-is
aliases:
  - grant
  - action
---

# Permission

## What it is

The per-key authorization grant attached to a `concept:api-key`. Each key carries a JSON array of grant entries; each entry is `{action, mode?, scope?}` — the action string (e.g. `instance:create`, `*:read`, per the wildcard grammar below), an optional `mode` (`execute` default | `dry_run`, an identity-bound dry-run floor owned by `concept:dry-run`), and an optional `scope` (a resource selector evaluated alongside the action match).

The grant comprises four pieces: the grant-entry types and their parser, the wildcard matcher and validator, the set-membership permission evaluator, and the canonical action registry.

## Purpose

The auth middleware needs a small, predictable grammar for "what this key is allowed to do." Forward-compatibility matters — entries grow new fields (`mode`, `scope` today; e.g. `rate_limit` later) without a schema migration — so entries are JSONB with a parser that preserves unknown fields.

## Boundaries

Owns: the grant entry shape, the wildcard matcher (`*`, `<noun>:*`, `*:<verb>`; colon retained as match boundary), the action registry's canonical V1 list, per-action *resource* scoping (the `scope` selector field and its scope-match semantics). Does NOT own: per-route handler dispatch (that's the HTTP router's concern), role expansion (CLI-side; see `concept:role-template`), the resolution of preview-vs-commit (`concept:dry-run` owns resolving the request's mode; `concept:permission` owns only the grant `mode` field that feeds the floor). Adjacent: `concept:api-key`, `concept:control-api`, `concept:dry-run` (orthogonal — the request flag, not a grant property), `concept:role-template`.

## Action grammar

Actions are `<noun>:<verb>` strings registered in the canonical action registry. Each action declares the HTTP routes and MCP tool names that map to it.

Wildcards (only at action boundaries):

- `*` — matches anything
- `<noun>:*` — matches any action starting with `<noun>:` (colon is part of the prefix)
- `*:<verb>` — matches any action ending with `:<verb>` (colon is part of the suffix)

No infix wildcards; no regex. `auth:*` matches `auth:create` but NOT `authority:create`.

## Scope match

A `scope` selector restricts the entry to requests whose target resource satisfies the selector (e.g. `{template_tag: "analytics"}` restricts a `template:register` grant to templates tagged `analytics`). Selector keys are per-action resource dimensions; an entry with no `scope` matches any target of its action.

## Invariants

- **Set-membership evaluation.** A request is allowed iff some entry's action matches AND that entry's scope (if present) is satisfied by the request's target resource; otherwise denied. Iteration order is irrelevant — any matching, in-scope entry allows, so there is no first-match-wins rule (it was only ever meaningful for resolving a per-entry mode).
- **Scoped entries are least-privilege.** A `scope`-bearing entry allows ONLY requests whose target resource satisfies the selector; an out-of-scope request of the same action is denied (403) unless another entry independently allows it.
- **Grant mode is a floor.** The matched entry's `mode` (default `execute`) is the most permissive mode the request may run at; the dry-run flag may restrict further but never escalate (see `concept:dry-run`).
- **Forward-compatible parser.** Unknown JSON fields on grant entries are preserved (round-tripped through marshal) so future fields aren't lost.
- **Action registry is canonical.** The same registry validates key-creation request bodies (unknown action strings → 400) and resolves MCP tool names → action → handler.
