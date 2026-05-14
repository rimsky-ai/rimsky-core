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

- Add a `StubModeProbeResponse` message to `protocols/proto/v1/executor.proto` with a `stub bool` field; require it in `attributes_delta` rather than a free-form map.
- Document the agreement explicitly in `docs/protocols/executor.md` "Stub mode signature" and `docs/concepts/conformance.md`.
- Expose a shared Go constant `conformance.StubAttributesDelta = map[string]any{"stub": true}` and reference it from both the probe and the runner.

## Evidence

- `_discover/2026-05-10-conformance-test-binaries.md` Observations bullet "stub-mode signature surface area".
- `cmd/rimsky-conformance-probe/main.go:80-84`.
- `conformance/runner.go::probeStubMode`.

