---
topic: conformance-test-binaries
kind: boundary
---

# Conformance suites ship as standalone binaries third parties run against their impls

## Description

A multi-protocol system with several reference implementations and an out-of-process plugin story needs a way for third-party implementers to validate their producers / executors / blob backends against rimsky's expectations. Rimsky ships four conformance binaries under `cmd/` plus two shared fixture packages — one for the on-the-wire executor protocol (`conformance/` at repo root) and one for the in-process foundation persistence interfaces (`foundation/persistence/conformance/`).

**The four binaries cover three distinct surfaces.** Two probe an external service over the wire (executor and claim-producer), one probes an in-process Go interface (blob backend), and one is a tightly-scoped pre-flight sidecar that the executor binary uses to assert the target is in stub mode before driving real scenarios:

- **`cmd/rimsky-executor-conformance`** (`main.go`, 113 lines) — exercises an executor against the supervisor's wire-protocol expectations. Configurable transport (`grpc` or `http+json`), endpoint, optional `--require-stub-mode`, `--scenarios`/`--skip` filters, optional `--check-observability` and `--check-lifecycle`. Imports `conformance/` (the executor scenario package) and `conformance/scenarios/` for the registered scenario set (one scenario per file: `execute_happy_path`, `async_handoff`, `cancel`, `heartbeats`, `terminal_is_last`, `stream_close_without_terminal`, `malformed_userdata`, `attributes_serialization`, `unknown_ack_id`). Lifecycle conformance reuses the executor-conformance dial path under the `--check-lifecycle` flag (the lifecycle protocol is a sibling to the executor protocol but doesn't need its own binary).
- **`cmd/rimsky-conformance-probe`** (`main.go`, 98 lines) — short-lived sidecar invoked by `rimsky-executor-conformance` (or runnable standalone) that issues *one* Execute RPC with `userdata: {stub_probe: true}` and asserts the executor's Complete event carries `attributes_delta = {stub: true}` (`main.go:56-87`). Spins up a `conformance.CallbackReceiver` so async-handoff executors can complete the probe via the callback path (`main.go:42-49`). Exits 0 only if the stub signature matches. The signal lets `rimsky-executor-conformance --require-stub-mode` refuse to issue real LLM calls.
- **`cmd/rimsky-claim-producer-conformance`** (`main.go`, 179 lines) — drives a claim-producer over gRPC via `foundation/integration/remote.Dial`. The check list is small and explicit (`main.go::RunClaimProducerConformance`, lines 88-179): `Capabilities`, `EnvelopeNonEmpty`, `OpenFirst` (single Open returns Available + a non-UNKNOWN RealizedWriteSemantics, and the value is in the advertised envelope), `OpenSecond` (a second Open with identical spec), `Uniformity` (when both Opens returned byte-equal Scope, RealizedWriteSemantics must be identical — the spec §2.5 invariant). Pick-policy producers that return non-byte-equal scopes across consecutive Opens skip the uniformity check rather than fail (`main.go:158-167`).
- **`cmd/rimsky-blob-backend-conformance`** (`main.go`, 251 lines) — distinct from the other three because the blob-backend surface is in-process Go (`persistence.BlobBackend`), not a wire protocol. The binary constructs the backend in-process by name (`memory | filesystem | pg-largeobject`) and runs six in-package checks: round-trip 1KB, round-trip 10MB, range read, delete-then-read-returns-`ErrBlobNotFound`, idempotent delete, concurrent writes (`main.go:115-138`). The `memory` backend's startup-time unified-only gate (see `2026-05-10-sqlite-dev-only` family) is bypassed by setting `RIMSKY_PROCESS_ROLE=unified` (`main.go:81-84`).

**Shared fixtures live in two packages.** `foundation/persistence/conformance/` (in-process persistence-interface fixtures: `acquisition.go`, `auto_terminal.go`, `claim_handles_update_scope.go`, `dispatch.go`, `instances_userdata_overrides.go`, `migrations.go`, `nodes_*.go`, `orphan.go`, `queue_in_tx.go`, `scope.go`, `sort_order.go`, `tx.go`, `verify.go`, `observability.go`) — imported by both driver-internal tests (`foundation/persistence/postgres/*_test.go` and `foundation/persistence/sqlite/*_test.go`) so the same scenarios validate the postgres and sqlite reference drivers. The blob-backend binary inlines its own tiny check set rather than reusing this package because the blob surface is its own contract. The repo-root `conformance/` package owns the wire-level executor scenarios and the `CallbackReceiver`, used only by the executor binary and the probe.

**Stub-mode gate relationships.** Of the four binaries, only `rimsky-executor-conformance` and `rimsky-conformance-probe` care about stub mode — the LLM-calling concern is executor-specific. `rimsky-claim-producer-conformance` uses a synthetic selector (`"rimsky/conformance/uniformity"`, `main.go:111-116`) and so makes no real-world calls even against a production producer. `rimsky-blob-backend-conformance` runs entirely in-process. The stub-mode gate is invoked at most once per conformance run: `rimsky-executor-conformance --require-stub-mode` calls the probe-equivalent in-process (`conformance/runner.go::probeStubMode`, lines 110+) before running any real scenario, returning false (with a clear error chain) if the executor either responded without the stub signature or never responded at all. The standalone `rimsky-conformance-probe` binary exists for operators who want to verify stub mode without driving the full scenario suite — typically wired into pre-deploy CI gates.

Conformance binaries are part of every release build — `Makefile`'s `build-all` target compiles them alongside the runtime binaries. They're packaged into Docker images per `deploy/build-images.sh` for CI consumption.

The binaries are deliberately separate from the in-repo `*_test.go` tests. A third-party implementer would have to import internal Go test code to use those; the conformance binaries are runnable artifacts that point at any endpoint and answer yes/no.

## Code surface

- `cmd/rimsky-executor-conformance/main.go` — executor conformance binary (113 lines).
- `cmd/rimsky-executor-conformance/observability_check.go` — `--check-observability` probe.
- `cmd/rimsky-executor-conformance/lifecycle_check.go` — `--check-lifecycle` probe.
- `cmd/rimsky-conformance-probe/main.go` — stub-mode probe (98 lines; emits `{stub_probe: true}` userdata, expects `attributes_delta = {stub: true}`).
- `cmd/rimsky-claim-producer-conformance/main.go` — producer conformance binary (179 lines, `RunClaimProducerConformance` is the entire check list).
- `cmd/rimsky-blob-backend-conformance/main.go` — blob backend conformance (251 lines; six in-package checks).
- `conformance/` (repo-root package) — executor wire scenarios + `CallbackReceiver`.
- `conformance/scenarios/` — one scenario per file (9 files), `init()`-registered.
- `conformance/runner.go` — `Run`, `probeStubMode`, `probeAsyncSupport`, `Summary`.
- `foundation/persistence/conformance/` — shared persistence-driver fixtures (21 .go files).
- `foundation/integration/remote/` — gRPC client the producer-conformance binary uses.
- `Makefile` `build-all` target.
- `executors/stub/` — the stub-mode reference impl.

## Prose surface

- `CLAUDE.md` "Build & test" — `rimsky-executor-conformance --require-stub-mode` example.
- `CLAUDE.md` "Non-obvious gotchas" — "Stub mode is required for conformance runs of LLM-calling executors."
- `docs/protocols/executor.md` and `docs/protocols/claim-producer.md` — point at the binaries as the conformance entry points.
- `.ok-planner/specs/2026-05-04-service-protocol-contract.md` — the contracts the conformance binaries check.

## Adjacent topics

- `2026-05-10-three-go-module-split` — conformance binaries cross module boundaries (cmd in root, fixtures in foundation).
- `2026-05-10-typescript-executor-claude-agent` — TS executor must pass the same conformance.
- `2026-05-10-out-of-process-claim-producers` — conformance is the only way to validate a third-party producer.
- `conformance-probe-stub-mode-handshake` — the probe-side detail.

## Observations

- Four binaries cover three protocols (executor, claim-producer, blob-backend) plus the probe sidecar; the lifecycle-subscriber protocol has no dedicated conformance binary (its idempotency is server-side in `rimsky_lifecycle_idempotency`, exercised by integration tests rather than third-party conformance). This is a documented asymmetry: lifecycle subscribers are simpler than executors or producers and don't have the same body of expected behaviors. The `--check-lifecycle` flag on `rimsky-executor-conformance` is the documented escape hatch.
- The stub-mode probe is a runtime gate, not a build-time gate. An operator can mistakenly point `rimsky-executor-conformance` at a production endpoint without `--require-stub-mode`; the safety net only fires when the flag is set.
- The conformance binaries are part of every release build, which means they impose a compile-time dependency on the protocols module. If a third-party implementer wants to run conformance against a release of rimsky, they download the conformance binary — they don't link against rimsky from Go code.
- The Makefile target `make build-all` compiles four binaries that an operator may never directly use; their value is realized at CI time. A future build configuration that separates "runtime" from "conformance" images might be reasonable.
- **Tension candidate (in-process vs wire asymmetry):** three of the four conformance binaries dial a remote service over gRPC/HTTP; `rimsky-blob-backend-conformance` constructs the backend in-process via a name → constructor switch and runs six tiny checks inlined into `main.go`. The shared `foundation/persistence/conformance/` package — which exists precisely to be runnable from both `*_test.go` and a binary — is NOT what the blob-backend binary imports. Two patterns for "the same kind of thing" (in-process driver/backend validation), one of them not reusing the shared fixture pattern, is a cold-read inconsistency.
- **Tension candidate (producer uniformity is conditional):** the producer-conformance binary's most subtle check (byte-equal scope ⇒ uniform RealizedWriteSemantics, `main.go:158-176`) is silently skipped for pick-policy producers because their scopes diverge across consecutive Opens. A reader looking at output that says `ok Uniformity` for one producer and `uniformity-untested-this-run: ...` for another can't tell which case they're in without re-reading the binary.
- **Tension candidate (stub-mode signature surface area):** the probe asserts `attributes_delta = {stub: true}` (`rimsky-conformance-probe/main.go:80-84`). Any executor that wants to be "stub-conformant" has to hard-code this exact map shape. The agreement is centralized only in the probe binary's source — no protobuf, no docs/protocols entry. Renaming the field would require simultaneous edits in the probe, the runner's `probeStubMode`, and every conformant executor.
