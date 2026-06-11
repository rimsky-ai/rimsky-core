---
decision: pre-v1-break-freely
status: as-is
---

# Pre-v1 stance

## Choice

No backwards-compat guarantee on wire / config / event-log / resource interface; delete dead code rather than carrying it forward.

## Rationale

No production data yet; cleaner refactors. This rule is replaced by deployed-stage rules when v1 ships.
