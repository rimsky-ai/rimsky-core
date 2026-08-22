---
decision: blob-backend-mismatch-read-refused
---

# A spilled row written under another blob backend refuses to read

## Choice

A read of a spilled attribute row whose handle names a backend other than the active one fails with an error naming the backend mismatch, on every read path — the attribute-row readers, the runtime scratch load, and attribute carry-forward — until the operator migrates the rows or restores the old backend. No path falls back to the inline data column (see `concept:blob-backend`).

## Rationale

A spilled row's inline column holds no value, so a fall-back hands an executor or a downstream node an empty attribute where a value belongs and reports no failure. A clear error at the read site tells the operator what happened and where.

## Alternatives

- Fall back to the inline column on a mismatch, favouring continuity — rejected: the inline column of a spilled row is empty, so the continuity is an empty value delivered silently.
- Split the rule by call site, so a current-run read errors and a historical copy degrades — rejected: two behaviours to state and test, and the degrading one still delivers nothing.
- A handle-rewriting backend migration tool that makes the mismatch unreachable — not taken now; if one lands, this decision is revisited to promise continuity through migration.
