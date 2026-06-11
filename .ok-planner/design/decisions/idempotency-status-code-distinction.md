---
decision: idempotency-status-code-distinction
status: as-is
---

# Operator-visible replay marker

## Choice

`201` on fresh insert vs `200` on replay (returning original `message_id`).

## Rationale

Distinguish fresh vs. replayed without body inspection.
