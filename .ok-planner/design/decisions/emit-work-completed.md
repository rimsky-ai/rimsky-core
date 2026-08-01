---
decision: emit-work-completed
status: as-is
---

# The ledger speaks both halves of work

## Choice

The terminal-application step appends a work-completed event carrying the same identifiers as its work-started twin plus the terminal kind (see `story:work-completed-emitted`, `concept:event-log`).

## Rationale

A declared-but-never-emitted event kind is a catalog lie, and completion is the half a duration-or-audit consumer needs.

## Alternatives

- Leave the kind declared but unemitted — rejected: a catalog kind no consumer can ever observe is a standing lie in the event catalog.
- Drop the kind from the catalog instead — rejected: duration and audit consumers need the completion half paired with its started twin, without reconstructing it from state-transition events.
