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

The safety against accidental real-money LLM calls in conformance runs is a runtime flag (`--require-stub-mode` + the probe sidecar). Without the flag, the probe doesn't fire, and conformance can run against any endpoint.

Default behavior is permissive: no probe runs unless the operator opts in. The right default for a stub-only executor (`executors/stub/`); a footgun for any LLM-calling executor.

## Why it matters

A new operator running `rimsky-executor-conformance --endpoint http://claude-agent` without reading the docs hits real APIs. Cost: real money. The safety net only fires when the operator already knows it should.

## Resolution candidates (do NOT pick)

- Reverse the default: the stub probe always runs unless the operator explicitly disables it.
- Detect known LLM-calling executors and require the opt-in for them.
- Add a documented protocol-level stub-mode declaration in the capabilities handshake so any LLM-calling executor self-declares.

## Evidence

- `_discover/conformance-probe-stub-mode-handshake.md` Description ("A conformance run without --require-stub-mode is allowed; the operator takes responsibility").
- `_discover/2026-05-10-conformance-test-binaries.md` Observations bullet 2.

