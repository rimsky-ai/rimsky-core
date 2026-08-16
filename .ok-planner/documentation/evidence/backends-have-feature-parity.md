---
trap: backends-have-feature-parity
release: d977250c
---
# Evidence set — SQLite and Postgres are interchangeable — every route, sweep, and retention setting behaves identically, so a dev stack on SQLite proves out a Postgres deployment.

Source of the prior: sibling-symmetry — `persistence.driver` with `persistence.postgres.*` and `persistence.sqlite.*` blocks presented as peers

## What the audit ran and observed (assumption record)

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

## Experiment record (experiment:assumption-backends-have-feature-parity)

# The same template on each driver, and one setting only one of them has

## What it ran against

Two `rimsky-all-in-one` containers from the tree's own image tag on a private
docker network, one on the SQLite driver and one on the Postgres driver against
a postgres container. Each is driven through the same script: register the same
template, deploy it, create an instance, wake the structural roots with an empty
message and post a typed one, wait for every frame to settle, then read the run
back. Two further containers set `persistence.blob.backend: pg-largeobject`, one
on each driver.

## What was observed

Seven checks, none failing.

Behaviour matched everywhere the run compared it. The template hashed to the
same id on both drivers. The settled run produced the same twenty-two events —
sixteen distinct `node_type|kind` pairs — on both. The nodes' run summaries,
the frames' states and the messages' types were identical. Six read routes —
the instance, its nodes, its frames, the event feed, the observability summary
and the observability health — answered with the same key structure on both.

Two things are not the same. The SQLite deployment warns at boot that the driver
is `for local development only — not supported for production. Use the postgres
driver for deployed rimsky instances`, and the Postgres deployment carries no
such line. And `persistence.blob.backend: pg-largeobject` — a persistence
setting like any other — stops the SQLite deployment at boot with `config:
pg-largeobject blob backend requires the postgres driver`, while the Postgres
deployment on that same setting comes up healthy.

Runnables: `src:.ok-planner/experiments/assumption-backends-have-feature-parity/` at the stamped commit.
