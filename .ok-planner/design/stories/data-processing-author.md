---
story: data-processing-author
status: as-is
---

# Claim-producer author writes typed-data mix-in

## Role

As a claim-producer author writing the typed-data mix-in, I can implement the `concept:data-processing` protocol — a capabilities advertisement, the begin-candidate / commit-candidate / abandon-candidate per-partition staging verbs, and the list-versions / list-partitions / get-version-schema listing verbs — advertised alongside my claim-producer protocol, with rimsky allocating staging candidates per fan-out partition, finalizing on success, garbage-collecting on failure, and surfacing version history through my listing surfaces, so that I support typed-data version lifecycle with partition-aware staging.

## Capability

Public typed-data mix-in protocol on a claim-producer (see `concept:data-processing`): per-partition staging via begin-candidate; finalize via commit-candidate; garbage-collect via abandon-candidate; version listing via list-versions, list-partitions, and get-version-schema.

## Business value

Claim-producer authors support typed-data version lifecycle with partition-aware staging — the same lifecycle a non-typed producer offers, plus version history surfaces.

