---
decision: fold-ownership-bail
status: as-is
---

# The verify-before-run bail path resolves through the unified engine

## Choice

The verify-before-run ownership-bail path resolves through the unified claim-handle resolution engine as its own source kind; no caller-owned per-claim Abandon plus claimant-guarded delete exists outside the engine for this path (see `concept:terminal-resolution`).

## Rationale

The path has rows and performs the engine's exact sequence; routing it outside the engine would duplicate the engine's shape, which is the duplicated-path disease this decision excludes.

## Alternatives

- A caller-owned per-claim Abandon plus claimant-guarded delete outside the engine — rejected: duplicates the engine's exact resolution sequence as a second path that must be kept in lockstep.
