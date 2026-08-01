---
concept: role-template
status: as-is
aliases:
  - bundled role
---

# Role template

## What it is

A CLI-bundled JSON resource that expands into a `concept:permission` grant at key-creation time. Six bundled templates ship with the CLI, spanning full platform access down to a single-action grant: `admin`, `operator`, `read-only`, `agent-supervisor`, `publisher-service`, `debug-operator`. The exact grant strings each name expands to are owned by the compiled-in role files, not enumerated here.

These are compiled into the CLI binary at build time and read on demand when a command needs them. Operators define custom roles as local JSON files and pass them via a role-file flag; the CLI loads a custom role the same way it loads a bundled one.

## Purpose

The server has no concept of roles — its only auth primitive is the per-key grant. The CLI provides the friendly layer: an operator names a bundled role plus per-grant overrides (e.g. an operator role with an additional grant for the auth-create action), the CLI assembles the expanded grant, and submits a key-creation request. The server stores the raw expanded grant; no role identifier is recorded server-side.

## Boundaries

Owns: the bundled JSON files, the CLI expansion logic, the grant-patch operators (an add-grant operator and a remove-grant operator on the CLI). Does NOT own: server-side authorization (that's `concept:permission`), preview-vs-commit (a per-request flag; see `concept:dry-run`). Adjacent: `concept:permission`, `concept:rimsky` (the CLI binary).

## Invariants

- **CLI-side only.** The server does not know roles exist. The CLI's key-detail surface may pattern-match a grant against bundled roles for display, naming the matching role on an exact match and reporting "custom" otherwise, but this is a display nicety; the wire surface is always the raw grant.
- **Operator-defined roles are local.** No server-side surface for "register a role with the cluster".
