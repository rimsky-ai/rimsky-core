---
issue: peer-auth-concept-omits-incremental-callback-bearer
kind: human
category: docs-drift
artifacts:
  - concept:peer-auth
  - concept:executor
status: repaired
opened: 2026-07-24T00:00:00Z
---

# The auth document a reader checks first implied a credential doesn't exist — it does

Question: does `concept:peer-auth`'s "peer identity replaces the run-token" claim correctly scope itself to the terminal callback, or does it read as covering every callback channel — including the two ongoing mid-dispatch channels (keepalive, attribute writeback) that still require a separate per-dispatch bearer token?

Rule that determined the fix: MECHANICAL-VS-JUDGMENT-RULE names "a stale sentence in one artifact aligned to the commitment the code and the counterpart artifact already agree on" as a mechanical corpus-side repair. `concept:executor` already states the incremental-callback bearer-token requirement correctly and completely ("The incremental per-run callbacks (keepalive and attribute writeback) additionally require the dispatch's cancel token presented as a bearer credential... layered under whatever transport-level peer-auth posture is configured"), and re-reading `lib/runtime/keepalive.go` / `lib/runtime/attribute_writeback.go` confirms the code still enforces this check. `concept:peer-auth` and `decision:run-token-swept` were the outliers, both silent on the exception — rescoping them to what `concept:executor` already commits to changes no commitment, only closes a gap between artifacts that already agree with the code.

What changed: in `.ok-planner/design/concepts/peer-auth.md`, rescoped the "Peer identity replaces the run-token" invariant to state it covers the terminal return leg only, with a pointer to `concept:executor` for the incremental-callback bearer-token carve-out. In `.ok-planner/design/decisions/run-token-swept.md`, added the same one-sentence carve-out to the Choice section.

How verified: cross-read `concept:executor`'s existing (correct) statement of the same fact and confirmed the new peer-auth/run-token-swept wording states nothing beyond it; documentation-only change, no build/test surface affected.
