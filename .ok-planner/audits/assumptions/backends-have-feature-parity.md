---
assumption: backends-have-feature-parity
commit: d977250c
disposition: trap
synthesized: 2026-08-16T05:48:16Z
---

# SQLite and Postgres are interchangeable — every route, sweep, and retention setting behaves identically, so a dev stack on SQLite proves out a Postgres deployment.

As operator choosing a database, I would take it that sQLite and Postgres are interchangeable — every route, sweep, and retention setting behaves identically, so a dev stack on SQLite proves out a Postgres deployment.

## Source

sibling-symmetry — `persistence.driver` with `persistence.postgres.*` and `persistence.sqlite.*` blocks presented as peers

## What a run would observe

run the same template through both drivers and compare route responses, event rows, and sweep behavior.

## Measured

Experiment `assumption-backends-have-feature-parity` (seven checks, none
failing) drove the same template through a SQLite deployment and a Postgres
deployment at this tree's tag and compared what came back, then set one
persistence key on each. The prior is right about the behaviour it names and
wrong as a universal.

Behaviour matched everywhere the run compared it: the same template id, the same
twenty-two events over sixteen distinct `node_type|kind` pairs, the same node
run summaries, frame states and message types, and the same key structure from
six read routes (instance, nodes, frames, event feed, observability summary,
observability health). A dev run on SQLite does reproduce the graph behaviour of
the same run on Postgres.

But the two are not interchangeable settings-for-settings, and the product says
so itself. `persistence.blob.backend: pg-largeobject` — a persistence setting
like any other — stops a SQLite deployment at boot with `config: pg-largeobject
blob backend requires the postgres driver`, while the Postgres deployment on the
same setting comes up healthy. And the SQLite deployment warns at every boot
that the driver is `for local development only — not supported for production.
Use the postgres driver for deployed rimsky instances`, a line the Postgres
deployment never prints. The run did not exercise sweeps or retention timing on
either driver, so those remain unmeasured; the universal is already refuted by
the setting that boots one and stops the other.
