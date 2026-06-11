---
decision: cold-read-style
status: as-is
---

# Code organization discipline

## Choice

File-by-feature; ~500-line file / ~100-line function guidelines; max 3 nesting levels via early returns; no base classes / DI containers / "Manager" abstractions.

## Rationale

Optimize for cold-read.
