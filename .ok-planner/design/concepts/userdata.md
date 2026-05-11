---
concept: userdata
status: as-is
aliases: []
references:
  - _discover/2026-05-10-opacity-of-userdata-claim-blob.md
  - _discover/2026-05-10-userdata-overrides-by-instance.md
---

# Userdata

## What it is

Userdata is an opaque per-node JSON blob the template author attaches and the executor consumes verbatim. Carried on `ExecuteRequest.userdata`. Never substituted. Never inspected by rimsky. Validated executor-side against the executor's declared `userdata_schema` if any.

## Purpose

Templates need a channel for "stuff the executor needs to read but rimsky shouldn't understand" — CLI flags, model selection, ad-hoc tuning, synthetic-blocker fields, tracing markers. Userdata is that channel; rimsky's only job is to forward the bytes.

## Boundaries

Owns: the bytes, the per-instance override merge mechanism, the routing-key validation. Does NOT own: substitution (see `attribute`), executor-side schema enforcement (see `executor.userdata_schema`), claim payload (see `claim`). Adjacent: `userdata-overrides`, `executor`, `opacity`.

## Invariants

- Userdata is opaque (`@blessed-invariant 11`). No substitution pass. No inspection. No validation beyond the executor-side schema check.
- `{{...}}` directives in userdata are literal text reaching the executor verbatim; the substitution grammar does not include a `{{userdata.*}}` source kind.
- Per-instance `userdata_overrides` validate only routing keys (`by_executor`, `by_node`, plus the executor/node names). Fragment values are never inspected.

## Aliases and historical names

CLAUDE.md "Common mistakes" calls out the confusion with cloud-init userdata (cloud-init parses; rimsky doesn't).

## Open within this concept

- The executor's `userdata_schema` (read by rimsky to validate userdata bytes at template-registration and dispatch time) is a sanctioned but unnamed exception to `@blessed-invariant 11` opacity — see `tensions/userdata-schema-as-opacity-exception.md`.

