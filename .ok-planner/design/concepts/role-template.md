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
- `agent-supervisor` — read across the platform + `node:reset`, `message:send` — the writes a supervisor agent realistically needs
- `publisher-service` — a single `message:send` grant; minimal grant for bundled publisher services
- `debug-operator` — `*:read` + `instance:pause`, `instance:resume`, `breakpoint:create`, `breakpoint:resume`, `breakpoint:delete` — debugger authority for pausing instances and managing runtime breakpoints

These ship embedded in the CLI binary and are loaded at startup. Operators can drop additional JSON files into a per-user roles directory or pass a role-file flag; the CLI loads them the same way.

## Purpose

The server has no concept of roles — its only auth primitive is the per-key grant. The CLI provides the friendly layer: an operator names a bundled role plus per-grant overrides (e.g. an operator role with an additional grant for the auth-create action), the CLI assembles the expanded grant, and submits a key-creation request. The server stores the raw expanded grant; no role identifier is recorded server-side.

## Boundaries

Owns: the bundled JSON files, the CLI expansion logic, the grant-patch operators (an add-grant operator and a remove-grant operator on the CLI). Does NOT own: server-side authorization (that's `concept:permission`), preview-vs-commit (a per-request flag; see `concept:dry-run`). Adjacent: `concept:permission`, `concept:rimsky` (the CLI binary).

## Invariants

- **CLI-side only.** The server does not know roles exist. The CLI's key-detail surface may pattern-match a grant against bundled roles for display ("role:operator + 1 override") but this is a display nicety; the wire surface is always the raw grant.
- **Operator-defined roles are local.** No server-side surface for "register a role with the cluster".
