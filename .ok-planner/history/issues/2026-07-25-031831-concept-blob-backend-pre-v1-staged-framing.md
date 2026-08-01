---
issue: concept-blob-backend-pre-v1-staged-framing
kind: audit
category: unclear
artifacts:
  - concept:blob-backend
status: repaired
opened: 2026-07-25T03:18:31Z
---

Question: does the blob-backend concept's "(e.g. after an operator switches backends pre-v1)" parenthetical improperly imply a post-v1 behavior change, and should the "pre-v1" qualifier be dropped?

Rule that determined the fix: `CURRENT-STATE-ONLY-RULE` bans roadmap language ("post-v1", "for now", "pre-v1") in design docs. The invariant's own preceding clause ("since a process can only reach the bytes of its own configured backend") states the architectural cause is structural, not temporary, and no other corpus document proposes a reconciliation tool for the post-v1 case — so nothing supports treating this as a genuine future gap; the rule forces dropping the qualifier, not filing a queued idea.

What changed: `.ok-planner/design/concepts/blob-backend.md` — removed "pre-v1" from the parenthetical, leaving "(e.g. after an operator switches backends)". No other change; the sentence's substance (skip-and-retain is permanent, cause is structural) was already correct and unchanged.

How verified: `grep -in pre-v1 .ok-planner/design/concepts/blob-backend.md` returns no matches. Markdown-only change, no build/test impact.
