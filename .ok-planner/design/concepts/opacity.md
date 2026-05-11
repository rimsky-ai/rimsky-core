---
concept: opacity
status: as-is
aliases:
  - inert bytes
references:
  - _discover/2026-05-10-opacity-of-userdata-claim-blob.md
  - _discover/2026-05-10-attribute-substitution-grammar.md
  - _discover/2026-05-10-blob-spill-pluggable-backends.md
---

# Opacity (cross-cutting discipline)

## What it is

A uniform discipline applied to four byte streams: userdata, claim payload/address/scope, blob content, named-event payloads. Each stream is "inert" in rimsky — never logged, formatted with `%v`, hashed for dedup, attached to traces, validated beyond schema gates, or included in error messages. Read only at sanctioned substitution sites.

## Purpose

Rimsky is a project-agnostic substrate. Logging, normalizing, or otherwise inspecting carrier bytes would couple rimsky to the carrier's semantics. The discipline keeps rimsky narrow: the bytes go in one side and come out the other unchanged, except at the precisely-named substitution leaf.

## Boundaries

Owns: the cross-cutting "don't inspect" rule, the enumerated sanctioned read sites, the per-stream invariant annotations. Does NOT own: any one of the streams individually (each has its own concept and schema home). Adjacent: `userdata`, `claim`, `scope`, `blob-backend`, `named-event`, `attribute` (substitution is the sanctioned exception).

## Invariants

Four `@blessed-invariant`s codify opacity:

- **§11** — userdata (`modeling/attribute/substitution.go:15-19`).
- **§20** — claim payload, address, scope (`foundation/locks/types.go:93-101`).
- **§21** — blob content (`foundation/persistence/blob.go:25-50`) and (by extension) named-event payloads.

Sanctioned read sites:

- `walkPath` (substitution leaf in `modeling/attribute/substitution.go`).
- `stringifyRaw` (same file; top-level address/scope directives).
- `makeStoreHandle` (wire-encoding into the executor's `google.protobuf.Struct` at `foundation/integration/runner_dispatch.go:710-770`).

## Aliases and historical names

None.

## Open within this concept

- "Single sanctioned introspection site" claim (substitution.go comment) vs three actual sites — see `tensions/substitution-introspection-site-count.md`.

