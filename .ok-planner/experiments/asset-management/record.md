---
experiment: asset-management
commit: PENDING
---

# Observing and governing the data assets an instance produced

## What it ran against

`run.py` builds `producer.go` — a claim producer written for this experiment
against the published claim-producer and data-processing gRPC protocols, which
advertises both in its capabilities handshake, mints a version id on every
Commit, and answers ListVersions from the versions it recorded — for Linux and
runs it in an `alpine` container on a private docker network. It then boots a
`rimsky-all-in-one` container from this tree's image on the same network with a
mounted `rimsky.yml` naming that producer, and drives the stack through the
shipped CLI and the control API. The template declares a node that
opens a durable-lifetime claim on the producer and a downstream node that reads
that node's output.

## What was observed

`rimsky asset list` returned exactly one asset, under the dotted node-type and
claim-alias form, against the producer that advertises data processing.
`rimsky asset show` carried `version_id: v1`, the id the producer minted at
Commit, with state `committed` and lifetime `durable`. `rimsky asset versions`
returned that version with its commit time and the producer metadata, so the
version history is the producer's own. The materialization-history route
returned the claim's terminal record, and every row carried record kind
`claim_terminal`. `rimsky asset lineage` returned the backward walk from the
asset, and the forward walk from the asset's materializing run — both
`/v1/lineage/runs/{id}/descendants` and `/v1/lineage/by-source/run/{id}` —
carried the downstream node's leaf run, so the operator can see what consumed
the asset before retiring it. `rimsky asset delete` succeeded and the asset no
longer appeared in the listing.

Ten checks, none failing.
