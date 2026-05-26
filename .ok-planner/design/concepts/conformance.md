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

Six thin CLI wrappers in `pkg:cmd/rimsky-*-conformance` over a shared library at `pkg:sdk/go/conformance` (one sub-package per protocol). Third-party service implementers download a conformance binary and point it at their service endpoint; Go service authors can also invoke the underlying library from a Go test without forking the binary.

- `cmd/rimsky-executor-conformance/main.go` — executor against `Executor.Execute`. Configurable transport (`grpc` / `http+json`), `--require-stub-mode`, `--scenarios`/`--skip` filters, `--check-observability`, `--check-lifecycle`. Registered scenarios live in `sdk/go/conformance/executor/scenarios/` (one per file: `execute_happy_path`, `async_handoff`, `cancel`, `heartbeats`, `terminal_is_last`, `stream_close_without_terminal`, `malformed_userdata`, `attributes_serialization`, `unknown_ack_id`). The six conformance binaries follow the generic naming pattern `rimsky-<protocol>-conformance` — the probe sidecar (`rimsky-conformance-probe`) is intentionally generic in name because it is protocol-agnostic (it's the in-process stub-mode probe, shared across all conformance binaries).
- `cmd/rimsky-conformance-probe/main.go` — stub-mode probe sidecar. Issues one Execute RPC with `userdata: {stub_probe: true}` and asserts the Complete event carries `attributes_delta = {stub: true}`. Spins up a `conformance.CallbackReceiver` (from `pkg:sdk/go/conformance/executor`) so async-handoff executors can complete the probe via the callback path.
- `cmd/rimsky-claim-producer-conformance/main.go` — claim-producer over gRPC. Runs the standard battery: `Capabilities`, `EnvelopeNonEmpty`, `OpenFirst` (single Open returns Available + a non-UNKNOWN RealizedWriteSemantics in the advertised envelope), `OpenSecond` (a second identical Open), `Uniformity` (byte-equal scope ⇒ identical RealizedWriteSemantics — spec §2.5), plus `SplitScope`/`ScopesConflict` (or their `Skipped` variants).
- `cmd/rimsky-blob-backend-conformance/main.go` — `BlobBackend` correctness via in-process construction. Six checks (round-trip 1KB, round-trip 10MB, range read, delete-then-read-returns-`ErrBlobNotFound`, idempotent delete, concurrent writes). The cmd binary adapts each concrete backend (memory / filesystem / pg-largeobject) from `pkg:foundation/persistence` to the SDK's reduced `Backend` interface so the in-process suite stays SDK-purity-clean.
- `cmd/rimsky-data-processing-conformance/main.go` — DataProcessing mix-in. Capabilities + per-materialization Begin → Commit + ListVersions / ListPartitions / GetVersionSchema smoke tests + concurrent-writes idempotency.
- `cmd/rimsky-publisher-conformance/main.go` — Publisher protocol. Capabilities + Subscribe + ListSubscriptions + SubscribeIdempotent + MessagePush (in-process receiver) + Unsubscribe + UnsubscribeIdempotent.
- `cmd/rimsky-validation-conformance/main.go` — Validation mix-in. Per-role happy-path + malformed-input + unknown-role checks.

The `pkg:sdk/go/conformance/` library lives in the peer Go module; the cmd binaries are thin (~30-100 lines each: parse flags, dial endpoint, invoke library, format output, exit). The legacy `conformance/` repo-root directory was retired during the 2026-05-24 SDK extraction.

## Purpose

A third-party implementer downloads a conformance binary and points it at their service endpoint. Pass/fail validates wire compatibility without forcing the implementer to import internal Go test code. Pre-2026-05-24, runner logic lived inline in each `cmd/rimsky-*-conformance/main.go`; post-2026-05-24 it's an importable Go library, so Go service authors can also invoke the same suite from their own Go tests against an in-process or testcontainers-hosted target.

## Boundaries

Owns: the conformance library (`pkg:sdk/go/conformance`), the thin CLI wrappers (`pkg:cmd/rimsky-*-conformance`), the two shared fixture packages, and the stub-mode probe (`pkg:cmd/rimsky-conformance-probe`). Does NOT own: in-repo `*_test.go` unit tests (those live with the source), the in-repo scenario harness under `graph/scenario.Start` (documented in CLAUDE.md "Build & test"), the lifecycle-subscriber protocol's own conformance (no dedicated binary; `--check-lifecycle` flag on `rimsky-executor-conformance` is the documented escape hatch, backed by `pkg:sdk/go/conformance/executor.RunLifecycleCheck`). Adjacent: `executor`, `claim-producer`, `blob-backend`, `sdk`.

## Invariants

- `rimsky-executor-conformance --require-stub-mode` issues an in-process probe equivalent to `rimsky-conformance-probe` at startup; non-stubbed LLM-calling executors fail before any real scenario runs.
- The stub-mode signature is the `attributes_delta = {stub: true}` map shape, centralized only in the probe binary's source. Any "stub-conformant" executor must hard-code this exact key/value pair.
- Conformance binaries are part of `make build-all` (compile-time dependency on the protocols module + the sdk/go module via go.work).
- LifecycleSubscriber has no dedicated conformance binary; its idempotency is server-side in `rimsky_lifecycle_idempotencies`, exercised by integration tests.
- `Uniformity` check is silently skipped (rather than failed) for pick-policy producers whose consecutive Opens return non-byte-equal scopes.
- The `memory` blob backend's startup-time unified-only gate is bypassed in `rimsky-blob-backend-conformance` by setting `RIMSKY_PROCESS_ROLE=unified`.

## Aliases and historical names

None live.

## Open within this concept

- Stub-mode env var + probe pairing is a runtime gate; build-time gating is not enforced — see `tensions/stub-mode-runtime-only-gate.md`.
- Blob-backend conformance inlines its six checks rather than reusing the shared `foundation/persistence/conformance/` fixture pattern — see `tensions/blob-backend-conformance-fixture-asymmetry.md`.
- Stub-mode signature (`attributes_delta = {stub: true}`) has no proto/doc surface — see `tensions/stub-mode-signature-no-proto-surface.md`.

## Notes

- Renamed executor-conformance binary per `spec:2026-05-12-nomenclature-resolution` (audit ride-along I.1). Binary naming standardized to the pattern `rimsky-<protocol>-conformance`; the probe sidecar retains its generic `rimsky-conformance-probe` name because it's protocol-agnostic.
- 2026-05-24: conformance runner logic extracted from pkg:cmd/rimsky-*-conformance/main.go into pkg:sdk/go/conformance as a library. CLI binaries kept as thin wrappers calling the library. External Go authors can now invoke conformance from a Go test. Also corrected pre-existing stale binary count (four → six) in the "What it is" section. See spec 2026-05-24-repo-reorganization-design phase P2.
