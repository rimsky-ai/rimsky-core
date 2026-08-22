---
concept: parked-state
aliases:
  - park
  - parked node
---

# Parked state

## What it is

Parked is one of the seven states a node-run passes through (see `concept:node-run`), entered from running when the executor ends its dispatch with a park outcome. A parked node is neither running nor failed: it holds, carrying a required wake time, executor-opaque scratch bytes, and tags, the single channel for operator- or executor-supplied annotation (see `concept:tag`). Parked belongs to the in-flight set: the run has not settled. Two paths end the hold: the wake time arrives, or a subscribed upstream settles and cascades to the parked receiver. A cascade wake returns the parked run to the re-eligible state and queues the cascade round as a new pending run, leaving the parked run's attribute bag and scratch untouched (see `concept:cascade`). Park is internal to a dispatch — the dispatch ends and the run continues — so a park emits an audit-only signal that fires no cascade and carries no attribute delta (see `concept:signal`), and a park writes nothing back to the run's attributes. A parked node carries no dedicated resume context; an executor that must thread its own state across the hold writes that state to the run's scratch and reads it back on the resume dispatch, because the same run re-dispatches rather than a copy of it. An executor waiting on an asynchronous callback is not parked: its node stays running for the length of the wait.

## Purpose

Parked gives a node a first-class way to hold for work that cannot finish inside one dispatch — a wait on a person, on a clock, or on an outside event — and to resume later without failing the run. The executor names when it wants waking, and a subscribed upstream can wake it sooner.

## Boundaries

Parked state owns the hold itself: the wake time a parked run carries, the scratch it carries across the hold, and the two paths out of the hold. It does not own the state machine parked sits in, which belongs to `concept:node-run`, nor the reasons a run leaves any state. It does not own the settlement of a held claim, which belongs to `concept:auto-terminal`, nor the reaping of orphaned claims, which belongs to `concept:orphan-reaper`. It does not own the attribute bag a resumed dispatch reads, which belongs to `concept:attribute`, nor the walk that wakes a parked receiver, which belongs to `concept:cascade`.

see also: `node-run`, `auto-terminal`, `claim-handle`, `cascade`, `signal`, `attribute`, `orphan-reaper`, `tag`

## Aliases

- park
- parked node
