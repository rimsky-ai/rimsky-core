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

The conformance library lives in the protocols module; each subcommand is a thin wrapper (parse flags, dial endpoint, invoke library, format output, exit). The conformance surface ships inside the single rimsky binary.

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
