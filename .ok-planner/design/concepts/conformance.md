---
concept: conformance
---

# Conformance

## What it is

A per-protocol conformance subcommand family on the CLI — one subcommand per
protocol — over a shared conformance library in the protocols module (one
sub-package per protocol). Third-party service implementers run a conformance
subcommand against their service endpoint; Go service authors can also invoke
the underlying library from a Go test without going through the CLI.

- Executor conformance — gRPC transport only (no HTTP+JSON bridge); the
  fail-closed stub-mode gate, scenario include/skip filters, and the
  observability check flag (see Invariants). Nine registered scenarios
  spanning the happy path, async handoff and restart survival, cancellation,
  attribute and terminal-tag round-trips, scratch park-and-resume, park
  emission, and malformed input.
- Stub-mode probe — its own subcommand, specific to the executor protocol,
  gRPC transport only; issues one Execute RPC and asserts the stub-mode
  response shape (see Invariants for the shared stub-mode signature). Spins up
  a callback receiver so async-handoff executors can complete the probe via
  the callback path.
- Claim-producer conformance over gRPC — the standard battery: capabilities,
  first- and second-open, cross-open uniformity, the split-scope and
  scope-conflict checks (including a raw-wire fallback probe against
  `supports=false` declarations — see Invariants), the terminal-verb checks
  (Commit, Abandon, Release, each with a repeat-call idempotency check), and
  the staged-async serialization check. An optional observability/
  retention-probe flag mirrors the executor subcommand's.
- Blob-backend conformance via in-process construction — ten checks
  (round-trip at two sizes, range read plus an out-of-bounds range probe,
  delete-then-read and delete-then-range-read, an empty-payload round-trip, a
  handle-shape check, idempotent delete, concurrent writes), run against each
  concrete backend (memory / filesystem / pg-largeobject) through the
  conformance library's reduced backend interface.
- DataProcessing-mix-in conformance — capabilities plus the candidate
  lifecycle (begin / commit / abandon, with idempotency and abandon-exclusion
  checks), version/partition/schema list smoke tests, and a concurrent-writes
  check.
- Publisher-protocol conformance — capabilities plus subscribe,
  list-subscriptions, idempotent-subscribe, message-push, unsubscribe, and
  idempotent-unsubscribe.
- Validation-mix-in conformance — per-role happy-path and unknown-role checks
  for every role, plus a malformed-input check for the executor role.
- Lifecycle-subscriber conformance over gRPC — a sanity pass issuing every
  template- and instance-lifecycle notification the protocol defines, each
  against synthetic identifiers, asserting the subscriber accepts all of them.

The conformance library lives in the protocols module; each subcommand is a
thin wrapper (parse flags, dial endpoint, invoke library, format output,
exit). The conformance surface ships inside the single rimsky binary.

## Purpose

A third-party implementer runs the per-protocol conformance subcommand against
their service endpoint. Pass/fail validates wire compatibility without forcing
the implementer to import internal Go test code. The runner logic lives in an
importable Go library, so Go service authors can also invoke the same suite
from their own Go tests against an in-process or testcontainers-hosted target.

## Boundaries

Owns: the conformance library, the per-protocol conformance subcommand
handlers, the shared fixture packages, and the stub-mode probe. Does NOT own:
in-repo unit tests (those live with the source), or the in-repo scenario
harness. Adjacent: `executor`, `claim-producer`, `blob-backend`, `publisher`,
`data-processing`, `validation`, `lifecycle-subscriber`.

## Invariants

- Every protocol carrying a conformance suite reaches it through exactly one
  CLI entry point: its own subcommand. No protocol's suite is reachable as a
  flag on another protocol's subcommand.
- The executor conformance subcommand always issues an in-process stub-mode
  probe before running scenarios, and the gate is fail-closed: a failed or
  negative probe stops the run before any scenario executes, unless the
  operator passes an explicit allow-live override. Under the override,
  stub-requiring scenarios skip rather than fail. Refusing a live endpoint is
  the default; there is no opt-in strict flag.
- The stub-mode signature — the stub-probe request flag, the stub
  attributes-delta response shape, and the sibling park- and cancel-probe
  flags — is defined once, in a shared definition in the conformance library
  that every issuing and asserting site imports: the conformance scenarios and
  the stub-capable in-tree executors alike. The signature is additionally
  documented as the contract a non-Go "stub-conformant" executor reproduces;
  it is not a wire-protocol field.
- The conformance surface is part of the all-targets build (compile-time
  dependency on the protocols module, carried by the rimsky binary).
- The uniformity check is silently skipped (rather than failed) for
  pick-policy producers whose consecutive opens return non-byte-equal scopes.
- The memory blob backend's startup-time unified-only gate is bypassed in the
  blob-backend conformance subcommand by running it under the unified process
  role.
- A claim producer that declares `supports=false` on the split-scope or
  scope-conflict capability is additionally probed on the raw wire, bypassing
  the conformance client's own capability short-circuit — this catches a
  fabricated success or a broken byte-equal implementation that the
  client-side fallback would otherwise mask.
