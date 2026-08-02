---
decision: cascade-flags-on-subscribes
---

# Cascade-behavior flags live on subscription entries

## Choice

Cascade-behavior flags (currently `force_upstream_refresh`) are fields of the subscription-entry shape, not of the substitution-ref shape and not of a separate block.

## Rationale

A single subscription edge can serve multiple substitution reads; placing flags per-ref creates a "which value wins?" ambiguity. The subscription is the edge.

## Alternatives

- Per-substitution-ref inline flags — rejected: ambiguity across multiple reads of the same sender.
- A separate `cascade_deps:` block — rejected: third surface for the same concept.
