---
decision: emit-work-completed
status: as-is
---

# The ledger speaks both halves of work

## Choice

The terminal-application step appends a `work_completed` event carrying the same identifiers as its `work_started` twin plus the terminal kind (see `story:work-completed-emitted`, `concept:event-log`).

## Rationale

A declared-but-never-emitted event kind is a catalog lie, and completion is the half a duration-or-audit consumer needs.
