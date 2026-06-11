---
decision: tracked-duplication
status: as-is
---

# Duplication discipline

## Choice

Prefer tracked duplication (`@source: path:func`, `@diverged: true` + `@reason:`) over hidden coupling; extract to shared only at 3+ identical stable sites under 50 lines.

## Rationale

Visible duplication is editable; abstractions hide intent.
