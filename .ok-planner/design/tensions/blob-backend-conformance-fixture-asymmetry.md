---
tension: blob-backend-conformance-fixture-asymmetry
category: inconsistent
status: open
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

A third-party `BlobBackend` implementer who wants to run the same checks under `go test` (instead of via the binary) has to either invoke `rimsky-blob-backend-conformance` as a subprocess or copy-paste the check logic. The two existing patterns — shared library function (`RunClaimProducerConformance`) or shared scenarios package (`foundation/persistence/conformance/`) — would both work. Two patterns for "the same kind of thing" (in-process driver/backend validation), one of them not reusing the shared fixture path, is the cold-read inconsistency.

## Resolution candidates (do NOT pick)

- Move the six blob-backend checks into a shared persistence-conformance fixture that both the conformance binary and the driver-internal tests consume, matching the pattern the persistence-driver fixtures already use (see `concept:conformance`).
- Expose a blob-backend conformance run as a library function, paralleling the claim-producer conformance entry point so a third-party implementer can invoke it under `go test`.
- Accept the inline-only arrangement as deliberate and record that decision at the blob-backend conformance binary, so the asymmetry with the other conformance suites is documented rather than incidental.

## Evidence

- `_discover/2026-05-10-conformance-test-binaries.md` Observations bullet "in-process vs wire asymmetry".
- `cmd/rimsky-blob-backend-conformance/main.go:115-138`.
- `foundation/persistence/conformance/` (21 .go files).

