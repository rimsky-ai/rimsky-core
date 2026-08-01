---
issue: decisions-corpus-wide-missing-proof-field
kind: audit
category: proof
artifacts:
  - design/decisions/*
status: answered
opened: 2026-07-24T00:00:00Z
---

# Do decisions still need a mandatory Proof section, and does the corpus need a 232-file catch-up sweep to add one?

No. The v14.1.0 authoring guidance retired the Proof section from the decision form entirely — the current decision template (`{{DECISION-TEMPLATE}}` in `.claude/skills/_shared/artifact-definitions.md`) is Choice/Rationale/Alternatives only, and a `## Proof` section on a decision is now itself a compliance violation. A decision's verification moved to the periodic implementation audit (`{{AUDIT-DEFINITION}}`, same file), written to `.ok-planner/audits/decisions/<slug>.md` by the `/verify-corpus` run rather than authored inline in the decision file. Corpus-wide check: 0 of 239 live decisions carry a `## Proof` heading — the filed gap no longer exists.
