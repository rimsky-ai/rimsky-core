---
decision: no-event-substitution
status: as-is
aliases: []
---

# No event-payload substitution path

## Choice

The substitution grammar has no per-emission event path: per-emission data lives in the attributes delta and is read through the node-attribute substitution path.

## Rationale

There is no named-event ledger for an event substitution path to read from, and per-emission data is honestly attribute data; a second substitution channel for the same bytes would duplicate the attribute path.

## Alternatives

- A dedicated per-emission substitution path backed by a named-event ledger — rejected: redundant with the attribute path once per-emission data lands in the attributes delta, at the cost of a ledger maintained solely to serve it.
