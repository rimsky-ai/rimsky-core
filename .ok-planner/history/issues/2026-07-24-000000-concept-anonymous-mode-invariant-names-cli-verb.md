---
issue: concept-anonymous-mode-invariant-names-cli-verb
kind: audit
category: muddy-boundary
artifacts:
  - concept:anonymous-mode
status: answered
opened: 2026-07-24T00:00:00Z
---

# Should a design doc name the exact command that silences the security banner?

Question: does `concept:anonymous-mode`'s banner invariant correctly avoid naming the specific CLI command that silences it, per the corpus's no-CLI-verb rule for concepts?

Answer: yes, already. Re-reading `.ok-planner/design/concepts/anonymous-mode.md`'s "Loud startup banner" invariant today, it reads "...that an operator-directed enable-authentication action stops the banner" — the generic form, naming no CLI verb. CONCEPT-DEFINITION (`.claude/skills/_shared/artifact-definitions.md`) states a concept "names the general property; the decisions name the instances that satisfy it," and no decision names this specific verb because which verb silences the banner (minting the first key) is not a tradeoff with a real alternative — the filed Problem's premise (the invariant names the command) had already rotted away by unlogged side-effect edit before this investigation, and the current wording squarely satisfies the rule.
