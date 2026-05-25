---
concept: role-template
status: as-is
aliases:
  - bundled role
references:
  - .ok-planner/specs/2026-05-15-control-plane-mcp-and-auth-design.md
---

# Role template

## What it is

A CLI-bundled JSON resource that expands into a `concept:permission` grant at key-creation time. The six V1-bundled templates live at `code:cmd/rimsky/roles/`:

- `admin.json` — full access (`[{ "action": "*" }]`)
- `operator.json` — operational verbs across the platform; can read auth state but cannot mutate keys
- `read-only.json` — `[{ "action": "*:read" }]`
- `agent-supervisor.json` — read across the platform + `node:invalidate`, `node:reset`, `message:send` — the writes a supervisor agent realistically needs
- `publisher-service.json` — `[{ "action": "message:send" }]`; minimal grant for bundled publisher services
- `debug-operator.json` — `*:read` + `instance:pause`, `instance:resume`, `breakpoint:create`, `breakpoint:resume`, `breakpoint:delete` — debugger authority for pausing instances and managing runtime breakpoints

Loaded via `code:cmd/rimsky/roles/embed.go::Load`. Operators can drop additional JSON files into a config directory (`~/.rimsky/roles/`) or pass `--role-file=<path>`; the CLI loads them the same way.

## Purpose

The server has no concept of roles — its only auth primitive is the per-key grant. The CLI provides the friendly layer: operators say "give me an `operator` key with `--add=auth:create`" and the CLI assembles the grant and POSTs to `/auth/keys`. The server stores the raw expanded grant; no role identifier is recorded server-side.

## Boundaries

Owns: the bundled JSON files, the CLI expansion logic (`code:cmd/rimsky/auth_create.go`), the patch operators (`--add`, `--remove`, `--dry-run`). Does NOT own: server-side authorization (that's `concept:permission`). Adjacent: `concept:permission`, `concept:rimsky` (the CLI binary).

## Invariants

- **CLI-side only.** The server does not know roles exist. `rimsky auth show <name>` may pattern-match a grant against bundled roles for display ("role:operator + 1 override") but this is a display nicety; the wire surface is always the raw grant.
- **Patch operators are CLI-side validated.** `--dry-run=<action>` rejects read actions (`*:read` suffix) and auth-mutation actions (`auth:create`, `auth:revoke`, `auth:rotate`) at CLI time; the server tolerates these for forward-compatibility but the handlers ignore dry-run mode anyway.
- **Operator-defined roles are local.** No server-side surface for "register a role with the cluster" in V1.

## Notes

- [2026-05-15] Concept introduced by spec `.ok-planner/specs/2026-05-15-control-plane-mcp-and-auth-design.md` ("Bundled role templates (CLI-side)").
- 2026-05-24 — Adds debug-operator role-template per spec 2026-05-24-instance-debugger-design. Bundles *:read, instance:pause, instance:resume, breakpoint:create, breakpoint:resume, breakpoint:delete. High-risk in production; grant explicitly. agent-supervisor unchanged.
