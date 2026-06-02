---
concept: conformance
status: as-is
aliases: []
---

# Conformance

## What it is

A `rimsky conformance <protocol>` subcommand family — one subcommand per protocol — over a shared conformance library in the protocols module (one sub-package per protocol). Third-party service implementers run a conformance subcommand against their service endpoint; Go service authors can also invoke the underlying library from a Go test without going through the CLI.

- Executor conformance — exercises an executor against its execute RPC. Configurable transport (gRPC or HTTP+JSON), a require-stub-mode flag, scenario include/skip filters, and observability/lifecycle check flags. The registered scenarios (one each) cover the happy path, async handoff, cancel, heartbeats, terminal-is-last, stream-close-without-terminal, malformed userdata, attributes serialization, and unknown-ack-id.
- Stub-mode probe — its own subcommand; protocol-agnostic. Issues one execute RPC carrying a stub-probe userdata flag and asserts the completion event carries the stub attributes-delta map shape. Spins up a callback receiver so async-handoff executors can complete the probe via the callback path.
- Claim-producer conformance over gRPC — runs the standard battery: capabilities, non-empty envelope, first-open (a single open returns available plus a known realized-write-semantics in the advertised envelope), second-open (a second identical open), uniformity (byte-equal scope ⇒ identical realized write semantics — spec §2.5), plus the split-scope and scopes-conflict checks (or their skipped variants).
- Blob-backend conformance via in-process construction — six checks (round-trip 1KB, round-trip 10MB, range read, delete-then-read-returns-not-found, idempotent delete, concurrent writes). The subcommand adapts each concrete backend (memory / filesystem / pg-largeobject) to the conformance library's reduced backend interface so the in-process suite stays protocols-purity-clean.
- DataProcessing-mix-in conformance — capabilities plus per-materialization begin→commit plus list-versions / list-partitions / get-version-schema smoke tests plus concurrent-writes idempotency.
- Publisher-protocol conformance — capabilities plus subscribe, list-subscriptions, idempotent-subscribe, message-push (in-process receiver), unsubscribe, and idempotent-unsubscribe.
- Validation-mix-in conformance — per-role happy-path plus malformed-input plus unknown-role checks.

The conformance library lives in the protocols module; each subcommand is a thin wrapper (parse flags, dial endpoint, invoke library, format output, exit). The conformance surface ships inside the single rimsky binary — there are no standalone per-protocol conformance binaries.

## Purpose

A third-party implementer runs `rimsky conformance <protocol>` against their service endpoint. Pass/fail validates wire compatibility without forcing the implementer to import internal Go test code. The runner logic lives in an importable Go library, so Go service authors can also invoke the same suite from their own Go tests against an in-process or testcontainers-hosted target.

## Boundaries

Owns: the conformance library, the `rimsky conformance <protocol>` subcommand handlers, the two shared fixture packages, and the stub-mode probe. Does NOT own: in-repo unit tests (those live with the source), the in-repo scenario harness, the lifecycle-subscriber protocol's own conformance (no dedicated subcommand; the lifecycle check flag on the executor conformance subcommand is the documented escape hatch, backed by a lifecycle-check entry point in the conformance library). Adjacent: `executor`, `claim-producer`, `blob-backend`.

## Invariants

- The executor conformance subcommand's require-stub-mode flag issues an in-process probe equivalent to the stub-mode probe at startup; non-stubbed LLM-calling executors fail before any real scenario runs.
- The stub-mode signature is the stub attributes-delta map shape, centralized only in the probe subcommand's source. Any "stub-conformant" executor must hard-code this exact key/value pair.
- The conformance surface is part of the all-targets build (compile-time dependency on the protocols module, carried by the rimsky binary).
- LifecycleSubscriber has no dedicated conformance subcommand; its idempotency is enforced server-side via a persisted idempotency ledger, exercised by integration tests.
- The uniformity check is silently skipped (rather than failed) for pick-policy producers whose consecutive opens return non-byte-equal scopes.
- The memory blob backend's startup-time unified-only gate is bypassed in the blob-backend conformance subcommand by running it under the unified process role.

## Aliases and historical names

The conformance surface was historically a set of standalone per-protocol binaries (named for the protocol they exercised), then thin CLI wrappers over the extracted runner library; both forms are retired. The conformance runners now ship as `rimsky conformance <protocol>` subcommands inside the single rimsky binary.

## Open within this concept

- Stub-mode runtime gate + probe pairing is a runtime gate; build-time gating is not enforced — see `tension:stub-mode-runtime-only-gate`.
- Blob-backend conformance inlines its six checks rather than reusing the shared persistence-conformance fixture pattern — see `tension:blob-backend-conformance-fixture-asymmetry`.
- The stub-mode signature (the stub attributes-delta map shape) has no proto/doc surface — see `tension:stub-mode-signature-no-proto-surface`.

## Notes

- Renamed the executor-conformance runner per `spec:2026-05-12-nomenclature-resolution` (audit ride-along I.1). The probe runner retains its generic protocol-agnostic name.
- 2026-05-24: conformance runner logic extracted from the per-protocol binaries into the SDK conformance library. The CLI form was kept as thin wrappers calling the library. External Go authors can now invoke conformance from a Go test. Also corrected a pre-existing stale runner count (four → six) in the "What it is" section. See `spec:2026-05-24-repo-reorganization-design` phase P2.
- 2026-05-25 — Codebase citations removed + cross-refs repaired for self-containment per spec:2026-05-25-concept-doc-self-containment.
- [2026-05-24] The host-agent-proxy is conformance-testable as a normal service via the existing executor and claim-producer conformance runners, run against the proxy with a stub spawned process behind an in-process agent fake. A dedicated host-agent conformance runner covering the agent ↔ proxy protocol from the agent side is a follow-up. Per spec 2026-05-24-host-agent-and-proxy-design.
- 2026-05-26 — conformance library moved from the SDK module into the protocols module as a sub-package; no API change. Per spec:2026-05-26-collapse-sdk-into-protocols.
- 2026-06-02 — the standalone per-protocol conformance binaries were folded into `rimsky conformance <protocol>` subcommands shipped in the single rimsky binary; the importable runner library is unchanged. Per `spec:2026-06-02-rimsky-core-remediation-design`.
