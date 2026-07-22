---
tension: per-cascade-source-mode
category: scope
status: open
affects:
  - cascade
  - wait-set
  - node-run
---

# Per-cascade-source-mode generalization

## What is muddy

The cascade-mode policy is currently one value per receiver template-node, applied uniformly across every cascade source feeding that receiver. A generalization would let a receiver carry different cascade-mode values per upstream sender — for example, coalescing attribute cascades from a high-frequency sender while preserving every message cascade from a low-frequency one.

The single-mode-per-node shape is sufficient for the workloads the design currently expresses, but it conflates "what dedup do I want for this dependency?" across heterogeneous upstreams. Whether the per-source generalization is the right scope expansion — and what its dispatcher / wait-set surface looks like — is undecided.

## Why it matters

Authors with mixed upstream cadences cannot express the natural pattern ("most-recent for the noisy attribute upstream, sequenced for the audit-trail message upstream") without splitting the receiver into two template-nodes. The split distorts the graph for a policy reason.

## Resolution candidates (do NOT pick)

- Keep cascade-mode as one value per receiver template-node; document the workaround pattern for mixed-cadence authors.
- Extend cascade-mode to a per-(receiver, sender-node) map, with a node-wide default and per-source overrides.
- Extend cascade-mode to a per-(receiver, signal-type) map, keyed by the signal taxonomy rather than the sender node.

## Evidence

- `decision:mode-default-most-recent` — current single-mode-per-node shape.
- `concept:wait-set` — the gate-evaluator surface that would carry the per-source mode lookup if the generalization landed.
