---
issue: decision-blob-backend-missing-proof-field
kind: audit
category: proof
artifacts:
  - decision:blob-backend
status: answered
opened: 2026-07-24T00:00:00Z
---

# Does decision:blob-backend need a Proof section covering the shipped default and spill threshold?

No. The corpus-wide Proof requirement this issue leaned on was retired in v14.1.0: the decision template (`{{DECISION-TEMPLATE}}` in `.claude/skills/_shared/artifact-definitions.md`) no longer has a Proof section at all, and verification of a decision's Choice is now the periodic implementation audit's job (`{{AUDIT-DEFINITION}}`, same file), recorded under `.ok-planner/audits/decisions/blob-backend.md` by `/verify-corpus` (not yet run — `.ok-planner/audits/` is currently empty, which is expected pending the first run). `decision:blob-backend` already carries a compliant Choice/Rationale/Alternatives body with no Proof section. Its sole `@decision: blob-backend` citation still sits on `cmd/rimsky/cli/compose/synthetic_config.go` (a config writer, not a check) — that remains a legitimate navigation annotation under the new regime, where annotations exist for navigation to enforcement sites, not as proof anchors.
