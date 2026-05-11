---
concept: conformance
status: as-is
aliases: []
references:
  - _discover/2026-05-10-conformance-test-binaries.md
  - _discover/conformance-probe-stub-mode-handshake.md
---

# Conformance

## What it is

Four standalone binaries under `cmd/` that exercise third-party peer implementations against rimsky's protocol expectations:

- `cmd/rimsky-conformance/main.go` (113 lines) — executor against `NodeExecutor.Execute`. Configurable transport (`grpc` / `http+json`), `--require-stub-mode`, `--scenarios`/`--skip` filters, `--check-observability`, `--check-lifecycle`. Registered scenarios live in `conformance/scenarios/` (one per file: `execute_happy_path`, `async_handoff`, `cancel`, `heartbeats`, `terminal_is_last`, `stream_close_without_terminal`, `malformed_userdata`, `attributes_serialization`, `unknown_ack_id`).
- `cmd/rimsky-conformance-probe/main.go` (98 lines) — stub-mode probe sidecar. Issues one Execute RPC with `userdata: {stub_probe: true}` and asserts the Complete event carries `attributes_delta = {stub: true}` (`main.go:56-87`). Spins up a `conformance.CallbackReceiver` so async-handoff executors can complete the probe via the callback path.
- `cmd/rimsky-claim-producer-conformance/main.go` (179 lines) — claim-producer over gRPC. `RunClaimProducerConformance` (lines 88-179) is the explicit check list: `Capabilities`, `EnvelopeNonEmpty`, `OpenFirst` (single Open returns Available + a non-UNKNOWN RealizedWriteSemantics in the advertised envelope), `OpenSecond` (a second identical Open), `Uniformity` (byte-equal scope ⇒ identical RealizedWriteSemantics — spec §2.5).
- `cmd/rimsky-blob-backend-conformance/main.go` (251 lines) — `BlobBackend` correctness via in-process construction. Six in-package checks (`main.go:115-138`): round-trip 1KB, round-trip 10MB, range read, delete-then-read-returns-`ErrBlobNotFound`, idempotent delete, concurrent writes.

Two shared fixture packages: `conformance/` (repo root) holds the wire-level executor scenarios and the `CallbackReceiver`; `foundation/persistence/conformance/` holds in-process persistence-driver fixtures shared by both postgres and sqlite driver-internal tests. All four binaries are part of `make build-all`.

## Purpose

A third-party implementer downloads a conformance binary and points it at their peer endpoint. Pass/fail validates wire compatibility without forcing the implementer to import internal Go test code.

## Boundaries

Owns: the standalone binaries, the two shared fixture packages, the stub-mode probe. Does NOT own: in-repo `*_test.go` unit tests (those live with the source), scenario harness (see `scenario-harness`), the lifecycle-subscriber protocol's own conformance (no dedicated binary; `--check-lifecycle` flag on `rimsky-conformance` is the documented escape hatch). Adjacent: `executor`, `claim-producer`, `blob-backend`, `scenario-harness`.

## Invariants

- `rimsky-conformance --require-stub-mode` issues an in-process probe equivalent to `rimsky-conformance-probe` at startup; non-stubbed LLM-calling executors fail before any real scenario runs.
- The stub-mode signature is the `attributes_delta = {stub: true}` map shape, centralized only in the probe binary's source. Any "stub-conformant" executor must hard-code this exact key/value pair.
- Conformance binaries are part of `make build-all` (compile-time dependency on the protocols module).
- LifecycleSubscriber has no dedicated conformance binary; its idempotency is server-side in `rimsky_lifecycle_idempotency`, exercised by integration tests.
- `Uniformity` check is silently skipped (rather than failed) for pick-policy producers whose consecutive Opens return non-byte-equal scopes (`rimsky-claim-producer-conformance/main.go:158-167`).
- The `memory` blob backend's startup-time unified-only gate is bypassed in `rimsky-blob-backend-conformance` by setting `RIMSKY_PROCESS_ROLE=unified` (`main.go:81-84`).

## Aliases and historical names

None live.

## Open within this concept

- Stub-mode env var + probe pairing is a runtime gate; build-time gating is not enforced — see `tensions/stub-mode-runtime-only-gate.md`.
- Blob-backend conformance inlines its six checks rather than reusing the shared `foundation/persistence/conformance/` fixture pattern — see `tensions/blob-backend-conformance-fixture-asymmetry.md`.
- Stub-mode signature (`attributes_delta = {stub: true}`) has no proto/doc surface — see `tensions/stub-mode-signature-no-proto-surface.md`.

