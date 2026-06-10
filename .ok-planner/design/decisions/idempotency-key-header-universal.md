---
decision: idempotency-key-header-universal
status: as-is
---

# Idempotency on message emit

## Choice

Mandatory `Idempotency-Key` HTTP header on `POST /instances/{id}/messages`.

## Rationale

Replay-safe by construction.

## Notes

2026-06-08 — Decision recorded via spec 2026-06-08-design-corpus-bootstrap.
