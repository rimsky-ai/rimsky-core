---
issue: concept-cascade-graph-enumerates-route-families
kind: audit
category: other
artifacts:
  - concept:cascade-graph
status: repaired
opened: 2026-07-25T03:18:31Z
---

Question: does the cascade-graph concept's "What it is" section, which listed nearly every route family by name, violate the house rule against concepts enumerating their own route-path instances — and if so, is the namesake cascade-graph join exempt?

Rule that determined the fix: `CONCEPT-DEFINITION`/`SELF-CONTAINMENT-RULE` (`.claude/skills/_shared/artifact-definitions.md`) explicitly names "route paths" as a forbidden concept-body enumeration; the corpus's own precedent (`concept:signal`, `concept:transition-reason`) resolves the same shape with "membership of the set is owned by the code, not enumerated here," naming individual members only where they carry a distinct commitment worth stating. The cascade-graph join is exactly such a commitment — it is the concept's namesake capability, not inventory — so the split the issue's Options section proposed is what the rule, applied via that precedent, forces.

What changed: `.ok-planner/design/concepts/cascade-graph.md` — replaced the flat "family of read endpoints covering observability summaries, the event feed, frames, ..." route-family list with a general description ("a read-only family of endpoints giving operators visibility into rimsky's own persisted runtime state ... and discovered peer status") plus the deferral sentence "Membership of the route family is owned by the control-api code, not enumerated here."; kept and centered the per-instance cascade-graph join description as the concept's namesake capability. Preserved the one specific behavioral fact the old paragraph carried (frames-read routes join each frame to its triggering message row) by moving it into `## Invariants` rather than dropping it. Regenerated the matching TOC line in `.ok-planner/design/concepts.md`.

How verified: re-read the edited file for internal consistency (Boundaries/Invariants still name the right owners); confirmed no other file cites the deleted route-family wording (`grep -rn "family of read endpoints" .ok-planner/design`). Markdown-only change, no build/test impact.
