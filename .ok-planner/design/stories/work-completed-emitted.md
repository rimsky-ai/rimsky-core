---
story: work-completed-emitted
status: as-is
---

# Operator pairs every work-started event with a work-completed event

## Role

As an operator or auditor reading the event log, I can pair every work-started event with a work-completed event, so durations and did-everything-finish audits are computable from the ledger.

## Capability

The terminal-application step appends a work-completed event carrying the same identifying fields as its work-started twin plus the terminal kind (see `decision:emit-work-completed`, `concept:event-log`).

## Business value

Run durations and did-everything-finish audits are computable from the ledger alone; the declared event-kind catalog matches what the ledger actually speaks.

## Acceptance

Dispatching a node-run appends a work-started event; the run reaching its terminal appends a work-completed event carrying the same identifying fields plus the terminal kind.

## Falsifier

Runs that reach terminal with no work-completed event in the ledger — the kind declared but never spoken.

## Proof

Executable proof — a scenario drives a run to terminal and asserts the paired events with matching identifiers.
