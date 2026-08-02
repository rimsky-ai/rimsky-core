---
story: data-processing-author
---

# Claim-producer author writes typed-data mix-in

## Story

As a claim-producer author writing the typed-data mix-in, I can implement the `concept:data-processing` protocol — a capabilities advertisement, the begin-candidate / commit-candidate / abandon-candidate per-partition staging verbs, and the list-versions / list-partitions / get-version-schema listing verbs — advertised alongside my claim-producer protocol, with rimsky allocating staging candidates per fan-out partition, finalizing on success, garbage-collecting on failure, and surfacing version history through my listing surfaces, so that I support typed-data version lifecycle with partition-aware staging.
