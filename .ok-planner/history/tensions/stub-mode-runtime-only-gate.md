---
tension: stub-mode-runtime-only-gate
category: unclear
status: open
affects:
  - conformance
  - executor
---

# `--require-stub-mode` is a runtime gate; an operator can mistakenly point conformance at a production endpoint without it

## What is muddy

The safety against accidental real-money LLM calls in conformance runs is a runtime flag, `--require-stub-mode`, gating an inline stub-mode probe run by the conformance CLI itself (the probe no longer lives in a separate sidecar binary). Without the flag, the probe doesn't fire, and conformance can run against any endpoint.

Default behavior is permissive: no probe runs unless the operator opts in. The right default for a stub-only executor (`executors/stub/`); a footgun for any LLM-calling executor.

## Why it matters

A new operator running the executor-conformance binary against an arbitrary endpoint without the stub-mode gate hits real APIs. Cost: real money. The safety net only fires when the operator already knows it should.

## Resolution candidates (do NOT pick)

- Reverse the default: the stub probe always runs unless the operator explicitly disables it.
- Detect known LLM-calling executors and require the opt-in for them.
- Add a documented protocol-level stub-mode declaration in the capabilities handshake so any LLM-calling executor self-declares.

## Evidence

- The standalone `rimsky-conformance-probe` sidecar binary this tension originally named no longer exists; the seven standalone conformance binaries were folded into `rimsky conformance <protocol>` subcommands, and the stub-mode probe now runs inline inside the executor conformance runner. The `--require-stub-mode` flag remains permissive-by-default (unset = no probe, any endpoint accepted) — no dossier ruling has flipped this default or otherwise closed the question.

