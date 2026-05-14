---
error: stub_mode_probe_failed
surfaced_to: operator
---

# Stub-mode probe failed

## What it means

`rimsky-executor-conformance --require-stub-mode` probed the executor at startup and found it not running in stub mode. Conformance refuses to issue real LLM calls.

## When it happens

When running conformance against an LLM-calling executor (claude-agent or similar) without setting the executor's stub-mode environment variable (commonly `RIMSKY_EXECUTOR_STUB_MODE=1`).

## What to do

Restart the target executor with stub mode enabled. The conformance harness's probe will accept the executor; conformance can then run without burning tokens.

## See also

- [`../../concepts/executor.md`](../../concepts/executor.md)
- [`../../protocols/executor.md`](../../protocols/executor.md)
