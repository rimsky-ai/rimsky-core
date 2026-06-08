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

A `scope` selector restricts the entry to requests whose target resource satisfies the selector (e.g. `{template_tag: "analytics"}` restricts a `template:register` grant to templates tagged `analytics`). Selector keys are per-action resource dimensions; an entry with no `scope` matches any target of its action (today's behavior).

## Invariants

- **Set-membership evaluation.** A request is allowed iff some entry's action matches AND that entry's scope (if present) is satisfied by the request's target resource; otherwise denied. Iteration order is irrelevant — any matching, in-scope entry allows, so there is no first-match-wins rule (it was only ever meaningful for resolving a per-entry mode).
- **Scoped entries are least-privilege.** A `scope`-bearing entry allows ONLY requests whose target resource satisfies the selector; an out-of-scope request of the same action is denied (403) unless another entry independently allows it.
- **Grant mode is a floor.** The matched entry's `mode` (default `execute`) is the most permissive mode the request may run at; the dry-run flag may restrict further but never escalate (see `concept:dry-run`).
- **Forward-compatible parser.** Unknown JSON fields on grant entries are preserved (round-tripped through marshal) so future fields aren't lost.
- **Action registry is canonical.** The same registry validates key-creation request bodies (unknown action strings → 400) and resolves MCP tool names → action → handler.

## Notes

- [2026-05-15] Concept introduced by `spec:2026-05-15-control-plane-mcp-and-auth-design` ("Permissions model").
- 2026-05-24 — Adds breakpoint:* and instance:pause / instance:resume action verbs to the canonical registry per `spec:2026-05-24-instance-debugger-design`. breakpoint:read covered by *:read wildcard; the four writes (create, resume, delete, instance:pause, instance:resume) require explicit grant via the new debug-operator role-template.
- 2026-05-25 — Codebase citations removed + cross-refs repaired for self-containment per spec:2026-05-25-concept-doc-self-containment.
- 2026-05-29 — Per `spec:2026-05-29-console-upstream-auth-audit-and-fixes`: a grant entry is now just an action string — the optional `mode` modifier is dropped entirely (preview-vs-commit is a per-request flag owned by `concept:dry-run`, not a grant property). The permission evaluator becomes **set-membership** (any matching entry allows); first-match-wins is removed as a concept, and the "Read actions ignore mode" and "Auth mutations are NOT dry-runnable" invariants are removed (both were mode-vocabulary statements). The wildcard grammar is unchanged. Adds `audit:read` to the canonical action registry (read of the `auth.*` audit rows — see `concept:event-log`), granted separately from `event:read` because audit data is sensitive; covered by the `*` (admin) and `*:read` (read-only) wildcards and granted explicitly to the operator role-template.
- 2026-06-06 — Per `spec:2026-06-06-comprehensive-gap-closure-design` (S-auth-identity-bound-dryrun, S-auth-grant-scope-enforced): restores the per-grant `mode` modifier (an identity-bound dry-run floor) and un-defers the 2026-05-15 V2 deferral of resource scoping — adds a `scope` resource-selector field with scope-match semantics evaluated alongside the action match, plus the least-privilege invariant.
- 2026-06-07 — Clarifies that the `compose:origin` capability action is matched by the same wildcard rules every other action uses, so an admin grant (`{"action": "*"}`) holds `compose:origin` by virtue of holding everything; non-wildcard non-compose-CLI keys do not hold it.
