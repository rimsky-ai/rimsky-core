---
concept: signal
---

# Signal

## What it is

A signal is the one emission shape for any transition that affects a node-run. Every signal is a type-path plus a typed payload: the type-path is canonical and hierarchical, and the payload is a structured object typed by that path.

Signals come in three top-level kinds. A terminal signal ends the run, and it has exactly two leaves: a success terminal, and an error terminal carrying an error class. A transient signal marks a moment where this dispatch concluded and the run carries on through another dispatch — a retry, an error caught mid-retry, a release and requeue, a wait on an asynchronous callback, or a park. An attribute-change signal reports one attribute key whose value differs from what the node's prior settled run left (see `concept:attribute`).

The transient kind carries one park leaf and no park-reason taxonomy; a park's reason rides its tags. A wait on an asynchronous callback is a transient of its own rather than a park, because the node stays running until the callback settles it.

An emitted signal feeds two consumers. The cascade walker selects candidate receivers by matching subscription edges against the type-path, then gates each candidate on a predicate evaluated over the payload. The walker consumes the payload and does not propagate it, so a subscriber receives the wake and not the payload (see `concept:wait-set`). The audit log records every signal as one row in the persisted audit-event ledger, keyed by the signal's type-path and carrying the signal's payload. That row is the one place a payload survives.

## Purpose

A signal gives cascade firing, audit, and subscription one vocabulary for what just happened to a node-run. One subscription surface — a type-path plus a predicate — lets an operator reason about every observable transition the same way. One audit vocabulary lets observability tooling say what happened without reading overlapping enumerations. One emission discipline lets a new transition become a first-class observable event without a new branch anywhere else.

## Boundaries

A signal owns the type-path taxonomy, the payload schema each type resolves to, the predicate language a subscription filters with — its environment, its compilation at registration, and its evaluation during the cascade walk — the pathway that writes each signal to the audit-event ledger, and the envelope construction every emission site shares.

A signal does not own the cascade walk or the subscription-edge map, which are `concept:node-subscription` and `concept:cascade`, both driven by signals. It does not own the wait-set ledger that drives dispatch eligibility, which is `concept:wait-set`. It does not own policy resolution — what the runtime produces on a given terminal kind — which is `concept:error-policy` and `concept:terminal-resolution`. It does not own the wire executor protocol: rimsky emits a signal from an executor's outcome, and the executor emits none.

A signal's type-path is the only audit-event kind for the transition it describes. `concept:transition-reason` is a narrower vocabulary the node-state machine consults to validate a transition; it is never written as an audit-event kind and carries no payload. Signal owns audit identity, and transition-reason owns state-machine validation.

See also: `concept:node-subscription`, `concept:cascade`, `concept:wait-set`, `concept:error-policy`, `concept:terminal-resolution`, `concept:event-log`, `concept:executor`, `concept:terminal-tag`, `concept:attribute`, `concept:transition-reason`.
