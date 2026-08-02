---
issue: intent-verifier-shape-checks-executor-uncited
kind: sprint
category: intent-ledger
artifacts:
  - concept:service
  - concept:executor
status: verified
opened: 2026-08-01T22:41:10Z
---

# The shipped shape-checks verifier is the only bundled service with no corpus presence

Rimsky ships a bundled shape-checks verifier — an executor that validates tabular data against declared checks (no-nulls, unique primary key, value-in-set, regex, numeric range) and also self-validates its own node configuration. It is a public surface a template author wires up like any other bundled service (`code:lib/services/executors/verifier-shape-checks/`). Zero live corpus files mention it; a validation dossier flagged it "awaiting promotion" in June and the promotion never happened.

Every structurally identical sibling — the HTTP verifier, the HTTP executor, the claude-agent executor, each sensor, each claim producer — has a story documenting its user-facing capability. The general concepts already cover the dual-role composition pattern (`concept:service`, `concept:executor`), so no concept gap exists; what's missing is the story that makes the capability discoverable and auditable. The story rules' discover-from-public-surfaces principle plus the catalog's otherwise universal pattern leave one compliant end state; adding a story is a sprint-level act. The consequence of doing nothing is concrete: the next whole-corpus audit reads a shipped public surface as unsupported-by-omission.

## Options

- Add a story for the shape-checks capability, modeled on the HTTP verifier's — restores the universal pattern; cost is one small artifact.
- Rule it below corpus altitude — makes this the single bundled public-surface service without a story, an inconsistency every future audit re-detects.

The ruling confirms the rule-forced addition. Note the sibling `issue:story-verifier-severity-allowlist-home` — the severity-partition story already covers one facet of this same executor; drafting should reconcile the two rather than minting overlapping stories.

## Ruling

> Generated ruling (/verify-issues): add a story documenting the shape-checks verifier's capability — a template author validates data shape with the built-in checks, no custom verifier to write — shaped like the HTTP verifier's story, and reconcile it with the existing severity-partition story so the executor's coverage is one coherent pair rather than an overlap. The catalog's discover-from-public-surfaces rule and its universal bundled-service pattern force the addition; only a sprint can make it.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
