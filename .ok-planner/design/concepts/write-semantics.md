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

A per-claim enum (`sync | staged_async | blocking_async | read_only`) that determines how the `ModeCoexists` matrix treats concurrent claims on byte-equal scope. Three-level structure: producer advertises an allowed-values set via `Capabilities()`; operator declares a narrowing allowed-values set per producer in `cfg:rimsky.yml` (key `write_semantics_allowed:`); each `Open` returns one realized value in `Acquired.realized_write_semantics`.

### Per-value semantics

The four realized values, with their concurrency consequences:

- **`sync`** — synchronous in-place writes; no staging area. Two writers on byte-equal scope conflict (`ModeCoexists` returns false for sync↔sync, sync↔staged_async, sync↔blocking_async). A read claim conflicts with a sync writer.
- **`staged_async`** — writes go to a producer-internal staging area; reads can dispatch concurrently with writes on the same scope (the reader sees the pre-stage snapshot). Two writers still conflict; reads and writers coexist. Honest support requires snapshot delegation or native MVCC pass-through — the reader-lease internal-serialization pattern is forbidden (`@blessed-invariant 9b`).
- **`blocking_async`** — staging area present, but reads block until commit. Two writers conflict; reads and writers serialize. The right answer when the producer can stage but cannot offer point-in-time snapshots to readers.
- **`read_only`** — read-only access; the producer will reject any write attempt. Two readers coexist trivially.

## Purpose

A single per-binary capability is too coarse (a postgres producer might support `sync` for some resources and `staged_async` for others); per-claim with no upper bound is unbounded. The three-level allowed-values structure pins what the producer claims to support, what the operator allows, and what each specific claim got.

## Boundaries

Owns: the enum values, the envelope handshake, the realized-per-claim value, the conflict-matrix input. Does NOT own: scope conflict comparison (see `scope`), claim disposition (see `claim-producer`), per-claim payload (see `claim`). Adjacent: `claim`, `claim-producer`, `scope`, `claim-handle`.

## Invariants

- Operator-declared `write_semantics_allowed` ⊆ producer-advertised set (validated at startup; fails fast).
- `UNKNOWN` is the proto zero value; producers must not return it; supervisor rejects it.
- Byte-equal-scope uniformity: two `Open` calls with byte-equal scope MUST return the same realized value (spec §2.5).
- Reader-lease internal serialization is forbidden for `staged_async` — honest support requires snapshot delegation or MVCC pass-through (`@blessed-invariant 9b`).

## Aliases and historical names

Pre-`spec:2026-05-12-nomenclature-resolution` Group C, the operator-facing YAML key was `write_semantics_envelope:` (with a single-value `write_semantics: <value>` shortcut accepted as a one-element list). Both forms are retired: the canonical key is `write_semantics_allowed:`, and the single-value shortcut is rejected with a precise error message. The proto field is `Capabilities.write_semantics_allowed` (renamed from `write_semantics_envelope`).

## Open within this concept

(none live; the `write_semantics_envelope` rename and the single-value alias retirement both landed under `spec:2026-05-12-nomenclature-resolution`.)

## Notes

- `write_semantics_envelope` → `write_semantics_allowed` rename per `spec:2026-05-12-nomenclature-resolution` Group C.2. Single-value `write_semantics:` YAML shortcut retired per Group C.1. Resolves `tension:_resolved/yaml-write-semantics-alias`.
- [2026-05-18] Folded content from former `docs/concepts/write-semantics.md` (now retired) — four-value plain-English breakdown (per-value semantics with concurrency consequences) added as a subsection under "What it is".

