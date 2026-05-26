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

Six thin CLI wrappers (one per protocol) over a shared SDK conformance library (one sub-package per protocol). Third-party service implementers download a conformance binary and point it at their service endpoint; Go service authors can also invoke the underlying library from a Go test without forking the binary.

- Executor conformance — exercises an executor against its execute RPC. Configurable transport (gRPC or HTTP+JSON), a require-stub-mode flag, scenario include/skip filters, and observability/lifecycle check flags. The registered scenarios (one each) cover the happy path, async handoff, cancel, heartbeats, terminal-is-last, stream-close-without-terminal, malformed userdata, attributes serialization, and unknown-ack-id. The six conformance binaries follow the generic naming pattern `rimsky-<protocol>-conformance` — the probe sidecar is intentionally generic in name because it is protocol-agnostic (it's the in-process stub-mode probe, shared across all conformance binaries).
- Stub-mode probe sidecar — issues one execute RPC carrying a stub-probe userdata flag and asserts the completion event carries the stub attributes-delta map shape. Spins up a callback receiver so async-handoff executors can complete the probe via the callback path.
- Claim-producer conformance over gRPC — runs the standard battery: capabilities, non-empty envelope, first-open (a single open returns available plus a known realized-write-semantics in the advertised envelope), second-open (a second identical open), uniformity (byte-equal scope ⇒ identical realized write semantics — spec §2.5), plus the split-scope and scopes-conflict checks (or their skipped variants).
- Blob-backend conformance via in-process construction — six checks (round-trip 1KB, round-trip 10MB, range read, delete-then-read-returns-not-found, idempotent delete, concurrent writes). The binary adapts each concrete backend (memory / filesystem / pg-largeobject) to the SDK's reduced backend interface so the in-process suite stays SDK-purity-clean.
- DataProcessing-mix-in conformance — capabilities plus per-materialization begin→commit plus list-versions / list-partitions / get-version-schema smoke tests plus concurrent-writes idempotency.
- Publisher-protocol conformance — capabilities plus subscribe, list-subscriptions, idempotent-subscribe, message-push (in-process receiver), unsubscribe, and idempotent-unsubscribe.
- Validation-mix-in conformance — per-role happy-path plus malformed-input plus unknown-role checks.

The SDK conformance library lives in the peer Go module; the binaries are thin wrappers (parse flags, dial endpoint, invoke library, format output, exit). The legacy repo-root conformance directory was retired during the 2026-05-24 SDK extraction.

## Purpose

A third-party implementer downloads a conformance binary and points it at their service endpoint. Pass/fail validates wire compatibility without forcing the implementer to import internal Go test code. Pre-2026-05-24, runner logic lived inline in each conformance binary; post-2026-05-24 it's an importable Go library, so Go service authors can also invoke the same suite from their own Go tests against an in-process or testcontainers-hosted target.

## Boundaries

Owns: the SDK conformance library, the thin CLI wrappers, the two shared fixture packages, and the stub-mode probe. Does NOT own: in-repo unit tests (those live with the source), the in-repo scenario harness, the lifecycle-subscriber protocol's own conformance (no dedicated binary; the lifecycle check flag on the executor conformance binary is the documented escape hatch, backed by a lifecycle-check entry point in the SDK library). Adjacent: `executor`, `claim-producer`, `blob-backend`, `sdk`.

## Invariants

- The executor conformance binary's require-stub-mode flag issues an in-process probe equivalent to the stub-mode probe at startup; non-stubbed LLM-calling executors fail before any real scenario runs.
- The stub-mode signature is the stub attributes-delta map shape, centralized only in the probe binary's source. Any "stub-conformant" executor must hard-code this exact key/value pair.
- Conformance binaries are part of the all-targets build (compile-time dependency on the protocols module plus the SDK Go module via the workspace).
- LifecycleSubscriber has no dedicated conformance binary; its idempotency is enforced server-side via a persisted idempotency ledger, exercised by integration tests.
- The uniformity check is silently skipped (rather than failed) for pick-policy producers whose consecutive opens return non-byte-equal scopes.
- The memory blob backend's startup-time unified-only gate is bypassed in the blob-backend conformance binary by running it under the unified process role.

## Aliases and historical names

None live.

## Open within this concept

- Stub-mode runtime gate + probe pairing is a runtime gate; build-time gating is not enforced — see `tension:stub-mode-runtime-only-gate`.
- Blob-backend conformance inlines its six checks rather than reusing the shared persistence-conformance fixture pattern — see `tension:blob-backend-conformance-fixture-asymmetry`.
- The stub-mode signature (the stub attributes-delta map shape) has no proto/doc surface — see `tension:stub-mode-signature-no-proto-surface`.

## Notes

- Renamed the executor-conformance binary per `spec:2026-05-12-nomenclature-resolution` (audit ride-along I.1). Binary naming standardized to the pattern `rimsky-<protocol>-conformance`; the probe sidecar retains its generic protocol-agnostic name.
- 2026-05-24: conformance runner logic extracted from the per-protocol binaries into the SDK conformance library. CLI binaries kept as thin wrappers calling the library. External Go authors can now invoke conformance from a Go test. Also corrected a pre-existing stale binary count (four → six) in the "What it is" section. See `spec:2026-05-24-repo-reorganization-design` phase P2.
- 2026-05-25 — Codebase citations removed + cross-refs repaired for self-containment per spec:2026-05-25-concept-doc-self-containment.
