---
tension: stub-mode-signature-no-proto-surface
category: unspecified
status: open
affects:
  - conformance
  - executor
---

# Stub-mode signature (`attributes_delta = {stub: true}`) is centralized only in the probe binary's source

## What is muddy

`rimsky-executor-conformance --require-stub-mode` (and the standalone `rimsky-conformance-probe`) asserts that a "stub-mode" executor responds to a probe Execute (`userdata: {stub_probe: true}`) with a Complete event whose `attributes_delta` is exactly `{stub: true}` (`cmd/rimsky-conformance-probe/main.go:80-84`, runner's `probeStubMode` at `conformance/runner.go:110+`).

Any executor that wants to be "stub-conformant" must hard-code this exact map shape. The agreement is not documented in `protocols/proto/v1/executor.proto`, not in `docs/protocols/executor.md`, not in `docs/concepts/conformance.md`, and has no shared constant in either the Go runner or the TS executor's source — both sides hard-code the literal `{stub: true}` independently.

A rename or shape change would require simultaneous edits in: the probe binary's expected-shape assertion, the runner's `probeStubMode`, every conformant executor's Go/TS code that emits the literal, and any third-party executor in private use.

## Why it matters

The stub-mode probe is the gating mechanism for `--require-stub-mode` — the safety net that prevents `rimsky-executor-conformance` from issuing real LLM calls against a production endpoint. A protocol-defining handshake that lives only in test binary source code is the kind of thing that breaks silently when someone "tidies up" the literal: the only signal is "conformance fails" with no clear pointer to the agreed shape.

This is the same shape as `async-callback-body-key` (the `type` vs `kind` issue) and `events-kind-no-enum`: a wire-level vocabulary that has no schema surface and lives only in code.

## Resolution candidates (do NOT pick)

- Give the stub-mode signature an explicit wire-protocol shape — a typed boolean field on the executor protocol — instead of asserting a free-form attribute map, so the handshake is part of the schema surface both sides depend on (see `concept:executor`).
- Document the stub-mode signature as part of the conformance concept's definition and the executor protocol's contract, so a conformant executor has an authoritative description of the expected shape (see `concept:conformance`).
- Centralize the expected stub-mode shape in one shared definition that both the conformance probe and the runner reference, so a rename or shape change has a single point of truth rather than independently hard-coded literals.

## Evidence

- `_discover/2026-05-10-conformance-test-binaries.md` Observations bullet "stub-mode signature surface area".
- `cmd/rimsky-conformance-probe/main.go:80-84`.
- `conformance/runner.go::probeStubMode`.

