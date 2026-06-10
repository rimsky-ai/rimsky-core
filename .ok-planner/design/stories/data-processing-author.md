---
story: data-processing-author
status: as-is
---

# Claim-producer author writes typed-data mix-in

## Role

As a claim-producer author writing the typed-data mix-in, I can implement the gRPC `DataProcessing` server (`Capabilities`, `BeginCandidate`, `CommitCandidate`, `AbandonCandidate`, `ListVersions`, `ListPartitions`, `GetVersionSchema`) advertised alongside my claim-producer protocol, with rimsky allocating staging candidates per fan-out partition, finalizing on success, garbage-collecting on failure, and surfacing version history through my listing surfaces, so that I support typed-data version lifecycle with partition-aware staging.

## Capability

Public `DataProcessing` mix-in protocol on a claim-producer: per-partition staging via `BeginCandidate`; finalize via `CommitCandidate`; GC via `AbandonCandidate`; version listing via `ListVersions` / `ListPartitions` / `GetVersionSchema`.

## Business value

Claim-producer authors support typed-data version lifecycle with partition-aware staging — the same lifecycle a non-typed producer offers, plus version history surfaces.

## Acceptance

A claim-producer advertising the `DataProcessing` mix-in is referenced from a template's fan-out node; rimsky calls `BeginCandidate` per sub-partition; the executor writes typed data via the returned candidate handle; on leaf success, rimsky calls `CommitCandidate` and the candidate's metadata surfaces in the parent writeback; on leaf failure, rimsky calls `AbandonCandidate` and the candidate is GC'd. `ListVersions` exposes finalized versions; `ListPartitions` exposes partitions per version; `GetVersionSchema` returns schema bytes.

## Falsifier

`BeginCandidate` is never called on a fan-out partition, OR `CommitCandidate` is called but the producer's effect is canned, OR `AbandonCandidate` is skipped on leaf failure, OR a declared version doesn't appear in `ListVersions`.

## Proof

Example.

## Notes

2026-06-08 — Story landed via spec 2026-06-08-design-corpus-bootstrap.
