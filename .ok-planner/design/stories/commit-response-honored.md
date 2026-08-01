---
story: commit-response-honored
status: as-is
---

# Claim-producer author's Commit response fields are honored

## Story

As a claim-producer author, I can set the version-id and producer-metadata fields on my base Commit response and see them land where the protocol says — the claim-handle row's version and the fan-out parent's writeback — so the fields the wire contract documents are real for the base protocol, not only for the data-processing mix-in.

The producer client returns the base Commit response body; the unified claim-handle resolution engine persists the response's version-id to the claim-handle row; the settle-children settlement path surfaces the producer-metadata field in the fan-out parent's writeback — as the claim-producer protocol's documentation promises (see `decision:wire-commit-response-fields`, `concept:child-execution`).

The wire contract is honest for the base protocol: producers that stamp version and metadata on Commit see them land without having to adopt the data-processing mix-in just to make the base-protocol fields take effect.
