---
concept: event-log
aliases:
  - audit log
---

# Event log (audit log)

## What it is

The event log is rimsky's own append-only ledger of what happened. Each entry carries its position in the ledger, a typed kind, a structured payload rimsky itself declares, the time it occurred, and — for an entry raised by work inside a running instance — the instance and the node that work belongs to. An entry raised by a surface outside any instance, such as a control-plane authentication check, names neither instance nor node. Kinds fall in two families: operational kinds drawn from a closed set the platform declares, and signal-class kinds carrying the canonical type path of the signal that produced them. Each operational kind has a writer (see `decision:event-log-kind-enum`). Rimsky's supervisor, scheduler, and control API write entries at observable transitions. The operator event feed reads them, and a separate audit-read surface, gated by a permission (see `concept:permission`), reads the control-plane authentication entries, filtered by who acted, what they attempted, on what, with what result, in which mode, and when.

## Purpose

The event log gives an operator a durable record of what the platform did, for incident review, dashboards, and debugging. Rimsky owns the record and reads it directly, so an operator asks questions of the ledger instead of reconstructing history from process output.

## Boundaries

The event log owns the ledger, what an entry records, and the read patterns that feed the operator surfaces. It does not own the trace-retention window that bounds how far back the ledger reaches: that window is one deployment-wide bound, shared with frames and node runs, applied here as a cutoff on an entry's occurrence time. It does not own what any individual kind means; the writer that emits a kind and the consumer that reads it carry that meaning. See also `concept:cascade-graph`, which exposes the operator event feed, `concept:observability`, and `concept:permission`.

## Aliases

- audit log
