---
decision: idempotency-status-code-distinction
status: as-is
---

# Operator-visible replay marker

## Choice

A created-resource status code on fresh insert versus an OK status code on replay (returning the original message identifier — see `concept:message`).

## Rationale

Distinguish fresh vs. replayed without body inspection.
