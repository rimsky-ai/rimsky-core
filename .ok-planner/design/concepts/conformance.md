---
concept: conformance
status: as-is
aliases: []
---

# Conformance

## What it is

A per-protocol conformance subcommand family on the CLI — one subcommand per protocol — over a shared conformance library in the protocols module (one sub-package per protocol). Third-party service implementers run a conformance subcommand against their service endpoint; Go service authors can also invoke the underlying library from a Go test without going through the CLI.

- Executor conformance — exercises an executor against its unary `Execute` RPC. Configurable transport (gRPC or HTTP+JSON), a require-stub-mode flag, scenario include/skip filters, and observability/lifecycle check flags. The registered scenarios (one each) cover the happy path, async handoff, async-callback survives supervisor restart, cancel, attributes serialization (the run-terminating Success carries the stub's `attributes_delta` map), tags round-trip on the settling Outcome, park-reason emission, malformed attributes, and unknown-ack-id.
- Stub-mode probe — its own subcommand, specific to the executor protocol (only the transport, gRPC or HTTP, varies). Issues one Execute RPC carrying a stub-probe flag in the request attributes and asserts the completion event carries the stub attributes-delta map shape. Spins up a callback receiver so async-handoff executors can complete the probe via the callback path.
- Claim-producer conformance over gRPC — runs the standard battery: capabilities, non-empty envelope, first-open (a single open returns available plus a known realized-write-semantics in the advertised envelope), second-open (a second identical open), uniformity (byte-equal scope ⇒ identical realized write semantics), the split-scope and scopes-conflict checks (or their skipped variants), the terminal-verb checks (Commit, Abandon, and Release each exercised via Open-then-verb, plus a retried terminal verb asserting idempotency), and the staged-async serialization check (a concurrent reader Open against an open writer must not block, ruling out the reader-lease pattern for staged_async). An observability/retention-probe flag is also available, mirroring the executor subcommand's observability check.
- Blob-backend conformance via in-process construction — six checks (round-trip 1KB, round-trip 10MB, range read, delete-then-read-returns-not-found, idempotent delete, concurrent writes). The subcommand adapts each concrete backend (memory / filesystem / pg-largeobject) to the conformance library's reduced backend interface so the in-process suite stays protocols-purity-clean.
- DataProcessing-mix-in conformance — capabilities plus per-materialization begin→commit, a begin-candidate idempotency-key check (a repeated BeginCandidate with the same idempotency key must return a byte-equal candidate handle), list-versions / list-partitions / get-version-schema smoke tests, and a concurrent-writes check asserting distinct version identifiers under concurrent commits.
- Publisher-protocol conformance — capabilities plus subscribe, list-subscriptions, idempotent-subscribe, message-push (in-process receiver), unsubscribe, and idempotent-unsubscribe.
- Validation-mix-in conformance — per-role happy-path plus unknown-role checks for every role, with an additional malformed-input check for the executor role.

The conformance library lives in the protocols module; each subcommand is a thin wrapper (parse flags, dial endpoint, invoke library, format output, exit). The conformance surface ships inside the single rimsky binary.

## Purpose

A third-party implementer runs the per-protocol conformance subcommand against their service endpoint. Pass/fail validates wire compatibility without forcing the implementer to import internal Go test code. The runner logic lives in an importable Go library, so Go service authors can also invoke the same suite from their own Go tests against an in-process or testcontainers-hosted target.

## Boundaries

Owns: the conformance library, the per-protocol conformance subcommand handlers, the shared fixture packages, and the stub-mode probe. Does NOT own: in-repo unit tests (those live with the source), the in-repo scenario harness, the lifecycle-subscriber protocol's own conformance (no dedicated subcommand; the lifecycle check flag on the executor conformance subcommand is the documented escape hatch, backed by a lifecycle-check entry point in the conformance library). Adjacent: `executor`, `claim-producer`, `blob-backend`, `publisher`, `data-processing`, `validation`, `lifecycle-subscriber`.

## Invariants

- The executor conformance subcommand always issues an in-process stub-mode probe before running scenarios. The require-stub-mode flag escalates a failed or negative probe result to a hard failure that stops the run before any scenario executes; without the flag, a failed probe instead causes every stub-requiring scenario to skip rather than fail.
- The stub-mode signature — the stub-probe request flag and the stub attributes-delta map shape — is hard-coded at each conformance-surface site that issues or asserts it, not centralized in one location. Any "stub-conformant" executor must hard-code this exact key/value pair.
- The conformance surface is part of the all-targets build (compile-time dependency on the protocols module, carried by the rimsky binary).
- LifecycleSubscriber has no dedicated conformance subcommand; its idempotency is enforced server-side via a persisted idempotency ledger, exercised by integration tests.
- The uniformity check is silently skipped (rather than failed) for pick-policy producers whose consecutive opens return non-byte-equal scopes.
- The memory blob backend's startup-time unified-only gate is bypassed in the blob-backend conformance subcommand by running it under the unified process role.
