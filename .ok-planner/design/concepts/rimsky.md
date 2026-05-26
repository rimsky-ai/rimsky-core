---
concept: rimsky
status: as-is
aliases:
  - rimsky-cli
references:
  - _discover/2026-05-10-rimsky-cli-thin-client.md
  - _discover/rimsky-cli-compose-prefix-reservation.md
  - .ok-planner/specs/2026-05-15-control-plane-mcp-and-auth-design.md
---

# rimsky (CLI)

## What it is

Thin HTTP+JSON client over the control-api. The CLI entrypoint is small; a client-builder layer assembles requests and the control-api serves them. Every CLI verb is one or more HTTP calls. Verb groups include `template`, `tag`, `instance`, `node`, `admin`, `messages`, `backfill`, `asset`, `lineage`, `parked`, `compose`, `dev`, `ctx`, and (added 2026-05-15) `auth`.

The binary was renamed `rimsky-cli` → `rimsky` by `spec:2026-05-15-control-plane-mcp-and-auth-design` ("CLI / Rename cutover"); no alias shim or compat symlink ships.

## Purpose

Operator tool of first resort. Thin pass-through means there's no client-side business logic duplicating server validation, and a new CLI release tracks the control-api routes by hand rather than via codegen.

## Boundaries

Owns: command-line UX, request building, the `compose:` prefix reservation discipline (client-side only), the bundled role definitions (see `concept:role-template`), resolution of `source_file:` references in spec YAML at template-register time, before the wire call that submits the template. The wire-side spec is always resolved bytes. Does NOT own: control-api routes (server-side), authentication enforcement (server-side; the CLI carries a Bearer token via a `--key` flag or an API-key environment variable). Adjacent: `concept:control-api`, `concept:tag`, `concept:instance`, `concept:api-key`, `concept:role-template`.

## Invariants

- HTTP+JSON only; no proto. The CLI assumes the routes it knows are present.
- `compose:<project>:<...>` tag and instance-key prefix reservation is enforced client-side only.
- The `compose` workflow uses the prefix to scan/diff/teardown project artifacts via the server's tag/key tables.
- **API key resolution**: every verb takes `--key=<token>` and falls back to an API-key environment variable. `auth status` and `auth init` tolerate a missing key (anonymous-mode bootstrap path); other verbs send the key as a Bearer token and surface 401 when missing.
- **`auth init` is special.** It posts a key-creation request without a Bearer token (anonymous-mode bootstrap) and refuses to run when any active key exists — the server's anonymous-mode predicate is the authoritative gate; the CLI's pre-check is a UX nicety.

## Subcommand groups

- **Dev loop**: `run`, `register`, `deploy`, `undeploy`, `instantiate`, `rm-instance`, `ls`, `logs`, `health`, `init`
- **Compose**: `compose`, `dev`
- **Literal API**: `template`, `tag`, `instance`, `node`, `admin`, `messages`, `backfill`, `asset`, `lineage`, `parked`
- **Context**: `ctx`
- **Auth** (added 2026-05-15): `auth init | create-key | list | show | revoke | rotate | status`

## Aliases and historical names

- `rimsky-cli` (pre-2026-05-15 binary name). The concept slug renamed to `rimsky` in lockstep.

## Open within this concept

- Client-side-only prefix reservation — see `tension:compose-prefix-client-side`.

## Notes

- [2026-05-15] Binary + concept slug rename and `auth` subcommand group added by `spec:2026-05-15-control-plane-mcp-and-auth-design`.
- 2026-05-19 — `source_file:` client-side resolution added per `spec:2026-05-19-multi-instance-template-ergonomics-design`.
- 2026-05-25 — Codebase citations removed + cross-refs repaired for self-containment per spec:2026-05-25-concept-doc-self-containment.
