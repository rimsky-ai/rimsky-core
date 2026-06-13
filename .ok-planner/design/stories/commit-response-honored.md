---
story: commit-response-honored
status: as-is
---

# Claim-producer author's Commit response fields are honored

## Role

As a claim-producer author, I can set `version_id` and `producer_metadata` on my base Commit response and see them land where the protocol says — the claim-handle row's version and the fan-out parent's writeback — so the fields the proto documents are real for the base protocol, not only for the data-processing mix-in.

## Capability

The producer client returns the base Commit response body; the unified claim-handle resolution engine persists the response's `version_id` to the claim-handle row; the settle-children settlement path surfaces `producer_metadata` in the fan-out parent's writeback — as the claim-producer protocol's documentation promises (see `decision:wire-commit-response-fields`, `concept:child-execution`).

## Business value

The wire contract is honest for the base protocol: producers that stamp version and metadata on Commit see them land without having to adopt the data-processing mix-in just to make documented fields real.

## Acceptance

A producer whose base-protocol Commit response carries a `version_id` sees it persisted on the corresponding claim-handle row; a fan-out whose children's commits carry `producer_metadata` sees it surfaced in the parent's writeback.

## Falsifier

Base-protocol Commit response fields set by the producer and absent from the row / writeback (response body discarded).

## Proof

Executable proof — a scenario with a stub producer that stamps both fields on the base Commit response asserts the persisted version and the writeback-surfaced metadata.
