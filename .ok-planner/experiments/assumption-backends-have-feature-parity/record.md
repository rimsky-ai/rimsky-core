---
experiment: assumption-backends-have-feature-parity
commit: PENDING
---

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
