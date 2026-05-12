---
concept: rimsky-cli
status: as-is
aliases: []
references:
  - _discover/2026-05-10-rimsky-cli-thin-client.md
  - _discover/rimsky-cli-compose-prefix-reservation.md
---

# rimsky-cli

## What it is

Thin HTTP+JSON client over the control-api. `cmd/rimsky-cli/main.go` is small; `modeling/cli/` builds requests; `modeling/controlapi/` serves them. Every CLI verb is one or more HTTP calls. Includes a `compose` subcommand for manifest-driven deployments.

## Purpose

Operator tool of first resort. Thin pass-through means there's no client-side business logic duplicating server validation, and a new CLI release tracks the control-api routes by hand rather than via codegen.

## Boundaries

Owns: command-line UX, request building, the `compose:` prefix reservation discipline (client-side only). Does NOT own: control-api routes (server-side), authentication (it just passes a bearer token through). Adjacent: `control-api`, `tag`, `instance`.

## Invariants

- HTTP+JSON only; no proto. CLI assumes the routes it knows are present.
- `compose:<project>:<...>` tag and instance-key prefix reservation is enforced client-side only.
- The `compose` workflow uses the prefix to scan/diff/teardown project artifacts via the server's tag/key tables.

## Aliases and historical names

None live.

## Open within this concept

- Client-side-only prefix reservation — see `tensions/compose-prefix-client-side.md`.

