---
decision: image-tagging-version-and-channel
status: as-is
---

# Image tag scheme

## Choice

An immutable per-version tag plus a mutable channel tag — one channel for formal releases and a separate channel for dev releases.

## Rationale

An immutable per-version tag keeps deployments reproducible and rollbacks exact; a mutable channel tag gives followers of each stream a low-friction current pointer without touching version pins.

## Alternatives

- Immutable tags only — rejected: every consumer of "current" must chase version bumps by hand.
- A single mutable latest tag — rejected: mixes dev and formal releases in one pointer.
