---
issue: rules-doc-jsonl-citation-ungated
kind: audit
category: doc-drift
artifacts:
  - story:rules-doc-accuracy
  - decision:doc-accuracy-gates
status: repaired
opened: 2026-08-02T09:58:04Z
---

# `rules.md` cited the retired `.ok-planner/issues.jsonl` path, and the accuracy gate couldn't see it

## Question

Did `.claude/rules/rules.md` correctly cite the current issue-intake location, and did the `decision:doc-accuracy-gates` mechanism (`tools/rulesdoc/rulesdoc_test.go::TestRulesDoc_CitedPathsExist`) actually catch it if not?

## Repair

Rule determining the fix: `decision:doc-accuracy-gates` commits to a mechanical, build-time diff between enumerating prose and the code facts it enumerates — the gap here was that the gate's own extension allowlist (`looksLikeRepoPath`) didn't recognize `.jsonl`-suffixed tokens as path-shaped, so the stale citation was invisible to the very check meant to catch it. Fixing both the stale sentence and the allowlist gap changes no commitment: the issue intake is already `.ok-planner/issues/` (a per-issue-file directory) per `CLAUDE.md`, the `ok-planner-cheatsheet.md`, and the actual directory contents; only the expression was wrong.

Changed:
- `.claude/rules/rules.md` (Writing & Analysis section): replaced the stale `` `.ok-planner/issues.jsonl` `` / "drained at `/sprint`" phrasing with the current `` `.ok-planner/issues/` `` / "resolved via `/plan-sprint`" phrasing.
- `CLAUDE.md` (repo root): the same stale `.ok-planner/issues.jsonl` phrasing existed there too (not gated by any test, but the same drift) — corrected to `.ok-planner/issues/` for consistency.
- `tools/rulesdoc/rulesdoc_test.go`: added `.jsonl` to `looksLikeRepoPath`'s recognized extension list, plus a `TestLooksLikeRepoPath` case (`.ok-planner/issues.jsonl` → `true`), so a `.jsonl`-shaped stale reference is recognized as path-shaped and fails the gate the next time it recurs.

Verified: `go test ./tools/rulesdoc/... -v` — `TestRulesDoc_CitedPathsExist` and `TestLooksLikeRepoPath` both pass.
