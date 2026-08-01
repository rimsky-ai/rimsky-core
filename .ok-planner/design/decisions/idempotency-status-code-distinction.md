---
decision: idempotency-status-code-distinction
status: as-is
---

# Operator-visible replay marker

## Choice

A created-resource status code on fresh insert versus an OK status code on replay (returning the original message identifier — see `concept:message`).

## Rationale

Distinguish fresh vs. replayed without body inspection.

## Alternatives

- One success status for both, with a replay marker in the body — rejected: forces callers to parse the body to detect a replay.
