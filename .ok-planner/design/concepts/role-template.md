---
concept: role-template
status: as-is
aliases:
  - bundled role
---

# Role template

## What it is

A CLI-bundled JSON resource that expands into a `concept:permission` grant at key-creation time. The six V1-bundled templates are:

- `admin` — full access (a single `*` action grant)
- `operator` — operational verbs across the platform; can read auth state but cannot mutate keys
- `read-only` — a single `*:read` grant
- `agent-supervisor` — read across the platform + `node:invalidate`, `node:reset`, `message:send` — the writes a supervisor agent realistically needs
- `publisher-service` — a single `message:send` grant; minimal grant for bundled publisher services
- `debug-operator` — `*:read` + `instance:pause`, `instance:resume`, `breakpoint:create`, `breakpoint:resume`, `breakpoint:delete` — debugger authority for pausing instances and managing runtime breakpoints

These ship embedded in the CLI binary and are loaded at startup. Operators can drop additional JSON files into a per-user roles directory or pass a role-file flag; the CLI loads them the same way.

## Purpose

The server has no concept of roles — its only auth primitive is the per-key grant. The CLI provides the friendly layer: operators say "give me an `operator` key with `--add=auth:create`" and the CLI assembles the grant and submits a key-creation request. The server stores the raw expanded grant; no role identifier is recorded server-side.

## Boundaries

Owns: the bundled JSON files, the CLI expansion logic, the grant patch operators (`--add`, `--remove`). Does NOT own: server-side authorization (that's `concept:permission`), preview-vs-commit (a per-request flag; see `concept:dry-run`). Adjacent: `concept:permission`, `concept:rimsky` (the CLI binary).

## Invariants

- **CLI-side only.** The server does not know roles exist. `rimsky auth show <name>` may pattern-match a grant against bundled roles for display ("role:operator + 1 override") but this is a display nicety; the wire surface is always the raw grant.
- **Operator-defined roles are local.** No server-side surface for "register a role with the cluster" in V1.

## Notes

- [2026-05-15] Concept introduced by `spec:2026-05-15-control-plane-mcp-and-auth-design` ("Bundled role templates (CLI-side)").
- 2026-05-24 — Adds debug-operator role-template per `spec:2026-05-24-instance-debugger-design`. Bundles *:read, instance:pause, instance:resume, breakpoint:create, breakpoint:resume, breakpoint:delete. High-risk in production; grant explicitly. agent-supervisor unchanged.
- 2026-05-25 — Codebase citations removed + cross-refs repaired for self-containment per spec:2026-05-25-concept-doc-self-containment.
- 2026-05-29 — Per `spec:2026-05-29-console-upstream-auth-audit-and-fixes`: removed the `--dry-run=<action>` grant-patch operator from Boundaries and deleted the invariant that it rejected read/auth-mutation actions because the handlers ignored dry-run mode. Both are now false — per-grant dry-run no longer exists (preview-vs-commit is a per-request flag; see `concept:dry-run`), so there is no `--dry-run` CLI operator, and auth mutations are now previewable via the request flag. The bundled role JSONs were already mode-free, so no entry text changed. The operator role-template now grants `audit:read` explicitly (see `concept:permission`).
