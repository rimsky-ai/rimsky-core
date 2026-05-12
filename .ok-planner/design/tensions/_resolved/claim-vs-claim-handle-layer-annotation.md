---
tension: claim-vs-claim-handle-layer-annotation
category: unclear
status: resolved
spec: 2026-05-11-design-log-convergence
affects:
  - claim
  - claim-handle
resolution:
  shape: add-layer-annotation
  summary: |
    Added one-line layer annotation at the top of both claim.md and
    claim-handle.md, naming the protocol-layer vs rimsky-persistence-layer
    split explicitly. Each concept's own Boundaries already implied the
    split; the annotation makes it the first thing a reader sees.
---

# `claim` and `claim-handle` are split by layer but the split is not stated up front

## What is muddy

`claim` and `claim-handle` are two concept files covering the same conceptual object at two layers: `claim` is the protocol-side noun (what `ClaimProducer.Open` returns: `ClaimResult` with payload, address, scope), and `claim-handle` is the rimsky-persistence-side noun (the `rimsky_claim_handle` row that records claim state for cascade, orphan reap, terminal resolution).

Both Boundaries sections imply the layer split, but neither states it directly. A reader new to rimsky who hits either file first does not learn that the other exists *for the same thing at a different layer*. The cleanest split in the catalog — but the implicit framing is a stumble point.

## Why it matters

The split is correct (different invariants apply at each layer: claim content is inert; claim-handle is rimsky's bookkeeping spine). But a reader has to triangulate to see why two concepts exist. This is a small Definition-level clarity gap, not a structural problem.

## Resolution candidates (do NOT pick)

- **Add a one-line annotation** at the top of both Definitions: "`claim` is the protocol-layer noun returned by `ClaimProducer.Open`; `claim-handle` is the rimsky-persistence-layer noun for the same conceptual thing. They have different invariants by layer."
- **Restructure** with a shared top-of-page banner across both files describing the layer split, then dive into per-layer detail.

## Evidence

- `concepts/claim.md` Definition + Boundaries.
- `concepts/claim-handle.md` Definition + Boundaries.
- `review-notes.md` "Possible merges / splits to reconsider" / "claim vs claim-handle" bullet.

