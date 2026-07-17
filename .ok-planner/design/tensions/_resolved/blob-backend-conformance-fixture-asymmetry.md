---
tension: blob-backend-conformance-fixture-asymmetry
category: inconsistent
status: resolved
affects:
  - conformance
  - blob-backend
---

# Three of four conformance binaries reuse shared fixture packages; the blob-backend binary inlines its six checks in `main.go`

## What is muddy

`rimsky-executor-conformance` and `rimsky-conformance-probe` share fixtures via the repo-root `conformance/` package (wire scenarios + `CallbackReceiver`). `rimsky-claim-producer-conformance` calls `RunClaimProducerConformance` exposed as a library function. Both of these patterns also serve in-repo `*_test.go` paths — the foundation persistence drivers (postgres + sqlite) share `foundation/persistence/conformance/` fixtures (21 .go files) between their driver-internal tests and any external suite.

`rimsky-blob-backend-conformance/main.go` (251 lines) breaks the pattern. The six checks (round-trip 1KB, round-trip 10MB, range read, delete-then-read-returns-`ErrBlobNotFound`, idempotent delete, concurrent writes) are inlined directly into `main.go:115-138` rather than residing in a shared fixture package that the binary and the driver-internal tests both consume.

The blob-backend surface is in-process Go (`persistence.BlobBackend`), not a wire protocol — but the same is true of the persistence-driver fixtures, which *do* reuse the shared pattern.

## Why it matters

A third-party `BlobBackend` implementer who wants to run the same checks under `go test` (instead of via the binary) has to either invoke `rimsky-blob-backend-conformance` as a subprocess or copy-paste the check logic. The two existing patterns — a shared library function exposed by the claim-producer conformance, or a shared scenarios package consumed by both the binary and the driver-internal tests — would both work. Two patterns for "the same kind of thing" (in-process driver/backend validation), one of them not reusing the shared fixture path, is the cold-read inconsistency.

## Resolution candidates (do NOT pick)

- Make `concept:conformance` own a shared blob-backend conformance fixture that the blob-backend conformance run and the driver-internal validation both consume, matching the reuse pattern the persistence-driver fixtures already follow.
- Extend `concept:conformance` so the blob-backend suite is reusable by a third-party `concept:blob-backend` implementer running it themselves, paralleling the reuse already afforded by the claim-producer conformance suite.
- Record in `concept:conformance` that the blob-backend suite is intentionally inline-only, so the asymmetry with the other conformance suites is a documented decision rather than incidental.

## Evidence

- The standalone `rimsky-blob-backend-conformance` binary no longer exists; conformance is now `rimsky conformance <protocol>` subcommands of the single CLI, and the blob-backend checks live in a shared fixture package exposing a `Run(ctx, backend) []CheckResult` entry point that both the CLI subcommand and any driver-internal test can call — the same reuse shape the other three protocol suites already followed.

## Resolution

The seven standalone `cmd/rimsky-*-conformance` binaries (blob-backend included) were folded into `rimsky conformance <protocol>` subcommands backed by an importable shared library under the protocols module's conformance package, so external Go implementers can invoke conformance from their own tests without shelling out (`concept:conformance`, 2026-05-24/27). The blob-backend suite now sits symmetrically alongside the claim-producer, executor, publisher, and data-processing suites as its own reusable fixture package — the asymmetry this tension flagged no longer exists.

