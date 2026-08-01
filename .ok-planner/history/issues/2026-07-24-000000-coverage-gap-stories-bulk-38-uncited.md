---
issue: coverage-gap-stories-bulk-38-uncited
kind: audit
category: proof
artifacts:
  - design/stories/*
status: answered
opened: 2026-07-24T00:00:00Z
---

# Does the corpus require every story to carry an `@story:` citation, making an uncited story a proof gap to drain?

No. Under the current guidance (`.claude/skills/_shared/artifact-definitions.md`, `{{STORY-DEFINITION}}` and `{{ANNOTATION-INTEGRITY-RULE}}`), a story carries no `Proof:` field and no proof artifacts; verification of a story is the periodic implementation-audit's job (`.ok-planner/audits/stories/<slug>.md`, produced by `/verify-corpus`), not a standing citation-coverage requirement. `@story:` annotations exist for navigation only ("Annotations carry exactly one job — navigation — and play no part in certification scope") and rollout is explicitly incremental — "every session leaves them as it works", never a bulk restoration pass. The rule the original filing leaned on (citation coverage as a proof obligation, with a missing tag read as a gap) was retired by this guidance change; there is nothing to drain and no standing check to add. Whether each of the named stories is actually supported by the codebase is instead the open question the first `/verify-corpus` run (not yet run — `.ok-planner/audits/` is currently empty) will answer per-story.
