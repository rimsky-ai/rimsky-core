---
concept: scenario-harness
status: as-is
aliases: []
references:
  - _discover/scenario-test-harness.md
  - _discover/2026-05-10-conformance-test-binaries.md
---

# Scenario harness

## What it is

`modeling/scenario/harness.go` — the entry point for in-repo scenario tests. Spins up a real Postgres via testcontainers, runs the migrate step, launches producer + executor peer-services on ephemeral gRPC ports, starts supervisor + scheduler + control-api wired to the test Postgres, and returns a handle the test can drive.

## Purpose

Blessed invariants need regression coverage against realistic flows: real Postgres, real gRPC peers, real concurrency. The harness makes that a one-line setup for test authors and is the reference for adding new invariant coverage.

## Boundaries

Owns: the bring-up logic, the `Start` entrypoint, the smoke fixture (`test/smoke/setup.go`). Does NOT own: conformance binaries (see `conformance`), unit tests (those live alongside source). Adjacent: `conformance`, `supervisor`, `persistence-driver`, `pgtest` (the allowed-internal fixture).

## Invariants

- Scenario tests use the public modeling-layer API + public peer protocols. They do not reach into `foundation/internal/` (depguard enforces).
- Each scenario boots its own Postgres container; tests are not unit-test fast.
- Race-sensitive paths get `-race -count=N` flake hunting.

## Aliases and historical names

None live.

## Open within this concept

(no live tensions)

