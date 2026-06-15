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

## Acceptance

A claim-producer advertising the typed-data mix-in is referenced from a template's fan-out node; rimsky calls begin-candidate per sub-partition; the executor writes typed data via the returned candidate handle; on leaf success, rimsky calls commit-candidate and the candidate's metadata surfaces in the parent writeback; on leaf failure, rimsky calls abandon-candidate and the candidate is garbage-collected. The list-versions verb exposes finalized versions; the list-partitions verb exposes partitions per version; the get-version-schema verb returns schema bytes.

## Falsifier

Begin-candidate is never called on a fan-out partition, OR commit-candidate is called but the producer's effect is canned, OR abandon-candidate is skipped on leaf failure, OR a declared version doesn't appear in the list-versions surface.

## Proof

Example.
