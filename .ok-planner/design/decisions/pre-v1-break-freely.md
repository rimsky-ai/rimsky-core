---
decision: pre-v1-break-freely
status: as-is
---

# Pre-v1 stance

## Choice

No backwards-compat guarantee on wire / config / event-log / resource interface; delete dead code rather than carrying it forward.

## Rationale

No production data yet; cleaner refactors.

## Alternatives

- Compatibility discipline from the start (deprecation cycles, migration shims across wire / config / event-log changes) — rejected: pure carrying cost with no production consumers to protect.
