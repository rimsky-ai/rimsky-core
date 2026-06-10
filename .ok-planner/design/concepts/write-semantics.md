---
concept: write-semantics
status: as-is
aliases: []
---

# Write semantics

## What it is

A per-claim enum (`sync | staged_async | blocking_async | read_only`) that determines how the coexistence matrix treats concurrent claims on byte-equal claim scope (per `concept:claim-scope`). Three-level structure: the producer advertises an allowed-values set through its capabilities; the operator declares a narrowing allowed-values set per producer in deployment config (the `write_semantics_allowed` setting); each open verb returns one realized value in its acquisition result.

### Per-value semantics

The four realized values, with their concurrency consequences:

- **`sync`** — synchronous in-place writes; no staging area. Two writers on byte-equal claim scope conflict (the coexistence predicate returns false for sync↔sync, sync↔staged_async, sync↔blocking_async). A read claim conflicts with a sync writer.
- **`staged_async`** — writes go to a producer-internal staging area; reads can dispatch concurrently with writes on the same claim scope (the reader sees the pre-stage snapshot). Two writers still conflict; reads and writers coexist. Honest support requires snapshot delegation or native MVCC pass-through — the reader-lease internal-serialization pattern is forbidden (`@blessed-invariant 9b`).
- **`blocking_async`** — staging area present, but reads block until commit. Two writers conflict; reads and writers serialize. The right answer when the producer can stage but cannot offer point-in-time snapshots to readers.
- **`read_only`** — read-only access; the producer will reject any write attempt. Two readers coexist trivially.

## Purpose

A single per-binary capability is too coarse (a postgres producer might support `sync` for some resources and `staged_async` for others); per-claim with no upper bound is unbounded. The three-level allowed-values structure pins what the producer claims to support, what the operator allows, and what each specific claim got.

## Boundaries

Owns: the enum values, the envelope handshake, the realized-per-claim value, the conflict-matrix input. Does NOT own: claim-scope conflict comparison (see `concept:claim-scope`), claim disposition (see `concept:claim-producer`), per-claim payload (see `concept:claim`). Adjacent: `concept:claim`, `concept:claim-producer`, `concept:claim-scope`, `concept:claim-handle`.

## Invariants

- The operator-declared allowed set ⊆ producer-advertised set (validated at startup; fails fast).
- `UNKNOWN` is the wire zero value; producers must not return it; the supervisor rejects it.
- Byte-equal-scope uniformity: two open-verb calls with byte-equal claim scope MUST return the same realized value.
- Reader-lease internal serialization is forbidden for `staged_async` — honest support requires snapshot delegation or MVCC pass-through (`@blessed-invariant 9b`).
