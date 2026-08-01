---
decision: wire-commit-response-fields
status: as-is
---

# Base Commit responses are read

## Choice

The producer client returns the base Commit response body; the unified claim-handle resolution engine persists `version_id` from the base Commit response to the claim-handle row; the settle-children path surfaces `producer_metadata` in the fan-out parent's writeback — as the claim-producer protocol's documentation promises (see `story:commit-response-honored`, `concept:child-execution`).

## Rationale

Contract-vs-runtime gaps close in the contract's favor when the contract is sensible; both sites sit on the claim-spine and child-execution seams that this decision's siblings already own.

## Alternatives

- Close the gap the other way — retract the documented promise that the base Commit response's fields are honored — rejected: version-id persistence and producer-metadata surfacing are those fields' whole purpose, and a documented-but-dropped response field teaches producer authors that the protocol's contract lies.
