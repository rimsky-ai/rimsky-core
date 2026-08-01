---
issue: stories-bare-proof-fields
kind: audit
category: compliance
status: answered
opened: 2026-07-25T21:11:30Z
---

# Twenty-nine stories have a category label where their Proof statement should be

Question: should the 29 stories whose `Proof:` field held only a category label ("Executable proof.", "Example.", "Demo.") get a substantive, third-party-observable Proof sentence?

The design corpus already answers this — the field the question is about no longer exists. Under the current STORY-DEFINITION (`.claude/skills/_shared/artifact-definitions.md`, v14.1.0): "A story carries no `Proof:` field and no proof artifacts... Verification is the audit's, and tests are ordinary tests." The suite converge on 2026-07-31 stripped `## Falsifier`/`## Proof` sections from every story file — re-verified live: `grep -l "^## Proof$" .ok-planner/design/stories/*.md` returns zero matches across all 123 files. Whether a story is supported by its tests is now the periodic `/verify-corpus` implementation audit's determination (recorded under `.ok-planner/audits/stories/<slug>.md`, currently empty pending the first run), not a hand-authored Proof sentence living in the story file. The filed gap ("a label instead of a substantive Proof sentence") cannot recur because the field it names is gone from the artifact shape.
