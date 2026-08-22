---
concept: data-processing
---

# Data processing

## What it is

Data processing is an optional mix-in protocol a claim producer may implement alongside the claim-producer protocol, advertising the mix-in in the same capabilities handshake. The mix-in carries the control plane of a typed-data version lifecycle. A run stages a candidate value against the claim it acquired, and the producer promotes the staged candidates into one canonical version when the parent claim commits. Data motion stays substrate-direct: a run reads and writes through the address its acquired result carries, so the protocol moves no data itself.

## Purpose

Data processing lets a claim producer that governs typed data expose that data's versioning to rimsky without exposing the data. Rimsky drives the staging and the promotion of a version as part of ordinary claim resolution. A template author gets an atomic many-writer write over data rimsky never reads, and the producer keeps its substrate, its storage layout, and its aggregation rules to itself.

## Boundaries

Data processing owns the mix-in's operation surface, the value-transition lifecycle of a producer candidate handle on each sub-claim, and the commit-aggregation step of parent-claim resolution. It does not own the per-child threading of a candidate handle through a fan-out dispatch (see `concept:fan-out`). It does not own the substrate the producer writes to, nor the aggregator vocabulary a claim names; rimsky interprets neither. It does not own how produced data is presented (see `concept:asset`).

see also: `claim-producer`, `asset`, `fan-out`, `validation`
