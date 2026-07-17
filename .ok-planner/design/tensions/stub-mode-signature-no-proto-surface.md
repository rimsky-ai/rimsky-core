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

`rimsky conformance executor --require-stub-mode` asserts that a "stub-mode" executor responds to a probe Execute (`userdata: {stub_probe: true}`) with a settling outcome whose `attributes_delta` is exactly `{stub: true}`. The probe logic is duplicated: once in the shared conformance runner library's `probeStubMode`, and again inline in the CLI's own conformance command (the standalone probe sidecar binary that originally hosted this assertion is gone).

Any executor that wants to be "stub-conformant" must hard-code this exact map shape. The agreement is not documented in `protocols/proto/v1/executor.proto` or in any in-tree protocol/conformance doc, and has no shared constant tying the two Go call sites together — each hard-codes the literal `{stub: true}` independently. There is no TypeScript executor in the tree to independently hard-code a second copy (all bundled executors, including the former TS claude-agent, are Go) — the "two languages hard-code it separately" framing is stale, but the "no schema surface, only code" problem is unchanged.

A rename or shape change would require simultaneous edits in: the probe binary's expected-shape assertion, the runner's `probeStubMode`, every conformant executor's Go/TS code that emits the literal, and any third-party executor in private use.

## Why it matters

The stub-mode probe is the gating mechanism for `--require-stub-mode` — the safety net that prevents `rimsky-executor-conformance` from issuing real LLM calls against a production endpoint. A protocol-defining handshake that lives only in test binary source code is the kind of thing that breaks silently when someone "tidies up" the literal: the only signal is "conformance fails" with no clear pointer to the agreed shape.

This is the same shape as `async-callback-body-key` (the `type` vs `kind` issue) and `events-kind-no-enum`: a wire-level vocabulary that has no schema surface and lives only in code.

## Resolution candidates (do NOT pick)

- Give the stub-mode signature an explicit wire-protocol shape — a typed boolean field on the executor protocol — instead of asserting a free-form attribute map, so the handshake is part of the schema surface both sides depend on (see `concept:executor`).
- Document the stub-mode signature as part of the conformance concept's definition and the executor protocol's contract, so a conformant executor has an authoritative description of the expected shape (see `concept:conformance`).
- Centralize the expected stub-mode shape in one shared definition that both the conformance probe and the runner reference, so a rename or shape change has a single point of truth rather than independently hard-coded literals.

## Evidence

- The standalone probe binary this tension originally cited is gone; the `{stub: true}` assertion now lives in two Go call sites — the shared conformance runner library's `probeStubMode` and a second inline copy in the CLI's conformance command — with no shared constant or protocol-level schema tying them together. No TS executor exists in the tree to hard-code a third copy.

