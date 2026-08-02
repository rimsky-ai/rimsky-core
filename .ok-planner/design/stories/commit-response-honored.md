---
story: commit-response-honored
---

# Claim-producer author's Commit response fields are honored

## Story

As a claim-producer author, I can set the version-id and producer-metadata fields on my base Commit response and see them land where the protocol says — the claim-handle row's version and the fan-out parent's writeback — so the fields the wire contract documents are real for the base protocol, not only for the data-processing mix-in.
