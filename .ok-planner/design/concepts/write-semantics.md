---
concept: write-semantics
status: as-is
aliases: []
references:
  - _discover/2026-05-10-write-semantics-envelope-handshake.md
  - _discover/2026-05-10-byte-equal-scope-conflict.md
  - _discover/2026-05-10-lock-state-in-rimsky-not-producer.md
---

# Write semantics

## What it is

A per-claim enum (`sync | staged_async | blocking_async | read_only`) that determines how the `ModeCoexists` matrix treats concurrent claims on byte-equal scope. Three-level structure: producer advertises an **envelope** (set of values) via `Capabilities()`; operator declares a narrowing envelope per producer in `rimsky.yml`; each `Open` returns one realized value in `Acquired.realized_write_semantics`.

## Purpose

A single per-binary capability is too coarse (a postgres producer might support `sync` for some resources and `staged_async` for others); per-claim with no upper bound is unbounded. The three-level envelope structure pins what the producer claims to support, what the operator allows, and what each specific claim got.

## Boundaries

Owns: the enum values, the envelope handshake, the realized-per-claim value, the conflict-matrix input. Does NOT own: scope conflict comparison (see `scope`), claim disposition (see `claim-producer`), per-claim payload (see `claim`). Adjacent: `claim`, `claim-producer`, `scope`, `claim-handle`.

## Invariants

- Operator envelope ⊆ producer envelope (validated at startup; fails fast).
- `UNKNOWN` is the proto zero value; producers must not return it; supervisor rejects it.
- Byte-equal-scope uniformity: two `Open` calls with byte-equal scope MUST return the same realized value (spec §2.5).
- Reader-lease internal serialization is forbidden for `staged_async` — honest support requires snapshot delegation or MVCC pass-through (`@blessed-invariant 9b`).

## Aliases and historical names

The legacy single-value `write_semantics:` YAML key is accepted as a single-element envelope shortcut (pre-v1 transition affordance).

## Open within this concept

- Legacy single-value `write_semantics:` YAML alias of `write_semantics_envelope:` — see `tensions/yaml-write-semantics-alias.md`.

