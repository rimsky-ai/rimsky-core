---
story: work-completed-emitted
status: as-is
---

# Operator pairs every work-started event with a work-completed event

## Story

As an operator or auditor reading the event log, I can pair every work-started event with a work-completed event, so durations and did-everything-finish audits are computable from the ledger.

The terminal-application step appends a work-completed event carrying the same identifying fields as its work-started twin plus the terminal kind (see `decision:emit-work-completed`, `concept:event-log`).

Run durations and did-everything-finish audits are computable from the ledger alone; the declared event-kind catalog matches what the ledger actually speaks.
