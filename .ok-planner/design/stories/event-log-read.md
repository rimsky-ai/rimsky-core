---
story: event-log-read
status: as-is
---

# Operator reads unified chronological event feed

## Role

As an operator, I can read the unified event log of an instance and see node lifecycle transitions, breakpoint hits, message activity, and supervisor decisions in true chronological order across kinds, with filtering by kind and time, so that I reconstruct what happened on an instance from one feed.

## Capability

Unified chronological reading of an instance's event log with cross-kind ordering and filter support, through the control-api or CLI.

## Business value

Operators reconstruct what happened on an instance from one feed in true chronological order, without source-grouping artifacts that hide cross-kind interleaving.

## Acceptance

Through the control-api or the event-log CLI surface (watch / logs verbs), an operator reads the events for an instance and observes a single timestamp-ordered feed where (for example) a breakpoint hit that occurred between two node-state transitions appears between them — not grouped by source — and where filtering by kind narrows to that kind while preserving the order across what remains.

## Falsifier

Events are returned source-grouped rather than timestamp-ordered, OR a breakpoint hit that actually occurred between two events appears outside the window.

## Proof

Executable proof.
