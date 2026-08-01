---
issue: anonymous-mode-non-template-sections
kind: human
category: corpus-hygiene
artifacts:
  - concept:anonymous-mode
  - story:anonymous-mode-bootstrap
status: repaired
opened: 2026-07-24T00:00:00Z
---

# Two operator how-to walkthroughs were living inside a definition document

Question: did the "Bootstrap sequence" and "Break-glass: lost admin key" sections in `concept:anonymous-mode` — two step-by-step operator walkthroughs — belong in a concept document, and if not, where?

Rule that determined the fix: CONCEPT-TEMPLATE (`.claude/skills/_shared/artifact-definitions.md`) fixes a concept body to What it is / Purpose / Boundaries / Invariants / Aliases — no procedural section exists in the template, and under v14.1.0 a story's only body is the single agile `Story` sentence (no acceptance section survives to receive step-by-step content either), so no artifact in the corpus has room for either walkthrough. Checking their content against the rest of the corpus: every fact in "Bootstrap sequence" was already stated elsewhere — the synthetic-identity mechanism and banner behavior in `concept:anonymous-mode`'s own What-it-is/Invariants, the plaintext-returned-once fact in `concept:api-key`'s invariants, and the open-then-locks-down capability in `story:anonymous-mode-bootstrap` — so it was dropped outright with no residue. "Break-glass" carried one fact stated nowhere else — that recovery from total key loss has no API/CLI path and requires direct database access — which was rephrased as a new `concept:anonymous-mode` invariant rather than dropped.

What changed: in `.ok-planner/design/concepts/anonymous-mode.md`, removed the "## Bootstrap sequence" and "## Break-glass: lost admin key" sections; added one invariant bullet ("No API-mediated recovery from total key loss...") capturing the sole fact from Break-glass that wasn't already stated elsewhere in the corpus. No CLI verb named. `story:anonymous-mode-bootstrap` and `concept:api-key` were left unchanged — they already fully covered the rest.

How verified: read the full corpus (concept:anonymous-mode, concept:api-key, story:anonymous-mode-bootstrap) to confirm every dropped sentence was duplicative before dropping it; this is a documentation-only change, no build/test surface affected.

Joint repair with `issue:concept-anonymous-mode-procedural-sections-off-template`, which targeted the same two sections.
