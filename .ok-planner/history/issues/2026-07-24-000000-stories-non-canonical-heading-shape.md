---
issue: stories-non-canonical-heading-shape
kind: human
category: docs-drift
artifacts:
  - design/stories/*
status: repaired
opened: 2026-07-24T00:00:00Z
---

# 122 of 123 story files ignore the official template — which side is right?

Question: should the 122 story files split across `## Role` / `## Capability` / `## Business value` headings (versus the STORY-TEMPLATE's single `## Story` heading) be swept to match the template, or should the template be changed to bless the house style?

This issue's own recommended ruling ("bless the house style, change the upstream template") is now moot: the filed premise — "the template lives in the planning toolchain, which the project's owner also controls, so change-the-rule is genuinely available" — has rotted. `.claude/skills/_shared/artifact-definitions.md` (v14.1.0) is now the fixed, current authority on artifact form and is not this project's to amend from within an issue-repair pass; its STORY-TEMPLATE is unambiguous: one `## Story` heading, one statement. The rule determines the fix, and reshaping headings while keeping every existing word changes no commitment — a mechanical repair, not a judgment call.

What changed: all 122 non-canonical files under `.ok-planner/design/stories/*.md` had their `## Role` / `## Role and capability` / `## Capability` / `## Business value` / `## Boundary` sub-headings removed and their body text concatenated, unedited, under one `## Story` heading — a pure heading-shape canonicalization, word-for-word lossless. `stories/sensor-object-store.md` was already canonical and untouched. No `design/` file's substantive content was altered; only section boundaries moved. (Whether the merged `## Story` sections still carry mechanism, delivery-surface, or config-key prescriptions inside that one heading is tracked separately by sibling issues `stories-name-rimsky-yml-and-config-keys`, `stories-delivery-surface-named-in-body`, and `stories-mechanism-prescription-tail` — out of scope for this heading-shape repair.)

How verified: `grep -h "^## " .ok-planner/design/stories/*.md | sort -u` now returns exactly `## Story` across all 123 files; a scripted diff confirmed no paragraph text was added, removed, or reordered relative to the pre-repair files (only heading lines were deleted); no blank-line artifacts introduced.
