---
concept: permission
status: as-is
aliases:
  - grant
  - action
references:
  - .ok-planner/specs/2026-05-15-control-plane-mcp-and-auth-design.md
---

# Permission

## What it is

The per-key authorization grant attached to a `concept:api-key`. Each key carries a JSONB array of `GrantEntry` objects:

```json
[
  { "action": "instance:create" },
  { "action": "node:invalidate", "mode": "dry_run" },
  { "action": "*:read" }
]
```

Implemented at `code:foundation/auth/grant.go` (grant types + parser) + `code:foundation/auth/action.go` (wildcard matcher + validator) + `code:foundation/auth/check.go` (first-match-wins permission evaluator) + `code:control/controlapi/actions.go` (the canonical action registry).

## Purpose

The auth middleware needs a small, predictable grammar for "what this key is allowed to do." Forward-compatibility matters — V2 needs to add `scope` / `rate_limit` fields without a schema migration — so entries are JSONB with a parser that preserves unknown fields.

## Boundaries

Owns: the grant entry shape, the wildcard matcher (`*`, `<noun>:*`, `*:<verb>`; colon retained as match boundary), the action registry's canonical V1 list. Does NOT own: per-route handler dispatch (that's chi), per-action *resource* scoping (V2 territory), role expansion (CLI-side; see `concept:role-template`). Adjacent: `concept:api-key`, `concept:control-api`, `concept:dry-run` (the per-entry mode modifier), `concept:role-template`.

## Action grammar

Actions are `<noun>:<verb>` strings registered at `code:control/controlapi/actions.go::v1Actions`. Each action declares the HTTP routes (chi patterns) and MCP tool names that map to it.

Wildcards (only at action boundaries):

- `*` — matches anything
- `<noun>:*` — matches any action starting with `<noun>:` (colon is part of the prefix)
- `*:<verb>` — matches any action ending with `:<verb>` (colon is part of the suffix)

No infix wildcards; no regex. `auth:*` matches `auth:create` but NOT `authority:create`.

## Invariants

- **First-match-wins evaluation.** Iteration order over the grant determines which entry's mode applies. Operators wanting a specific mode override (e.g. `{instance:create, mode: dry_run}`) place it before the wildcard fallback.
- **Forward-compatible parser.** Unknown JSON fields on grant entries are preserved in `Extras` and round-trip through marshal so future fields don't get lost.
- **Read actions ignore `mode`.** The dry-run modifier is meaningful only for write actions.
- **Auth mutations are NOT dry-runnable.** The handlers at `code:control/controlapi/auth_handlers.go::handleCreateKey` / `handleRevokeKey` / `handleRotateKey` always execute regardless of the matching grant entry's mode — dry-runs interact badly with the implicit-anonymous predicate.
- **Action registry is canonical.** The same registry validates `POST /auth/keys` request bodies (unknown action strings → 400) and resolves MCP tool names → action → handler.

## Notes

- [2026-05-15] Concept introduced by spec `.ok-planner/specs/2026-05-15-control-plane-mcp-and-auth-design.md` ("Permissions model").
- 2026-05-24 — Adds breakpoint:* and instance:pause / instance:resume action verbs to the canonical registry per spec 2026-05-24-instance-debugger-design. breakpoint:read covered by *:read wildcard; the four writes (create, resume, delete, instance:pause, instance:resume) require explicit grant via the new debug-operator role-template.
