---
issue: concept-anonymous-mode-invariant-names-cli-verb
kind: audit
category: muddy-boundary
artifacts:
  - concept:anonymous-mode
status: verified
opened: 2026-07-24T00:00:00Z
---

# Should a design doc name the exact command that silences the security banner?

Rimsky's anonymous-mode design document (describing a fresh deployment with no admin credentials yet) commits, as an invariant, that a warning banner fires repeatedly while the deployment is unsecured and that an operator action stops it. When this issue was filed, the invariant named the exact CLI command; the corpus's style rule says definition documents must not enumerate CLI verbs — implementation specifics live in code or, when a real tradeoff was involved, in a narrower "decision" document. The wrinkle: since filing, the wording was already rewritten to the generic form ("an operator-directed action stops the banner") — as an unlogged side effect of unrelated work, not a deliberate resolution. The question survives the rot: is "the banner names one specific, canonical remediation" a property worth stating precisely, or is the generic wording the right altitude, with the exact command living only in the code where the banner message is defined?

Two sibling issues cover other sections of the same document; this one is narrower (one line in an already-compliant section) and needs no joint ruling.

## Options

- **Accept the current generic wording** — matches the style rule; a reader can no longer tell from the doc that exactly one blessed command exists.
- **Revert to naming the command** — precision, at the cost of the doc becoming a second source of truth for banner wording that already drifted once.
- **Record the specific command in a decision document** — a citable home without breaking the rule; only justified if choosing that command was a real tradeoff rather than the obvious choice.

The ruling decides: which altitude wins; whether the command deserves any documented home beyond the code; and whether the unlogged rewrite is ratified or flagged as process drift.

## Ruling

> Recommended ruling (/recommend-rulings): retire: the current generic
> wording ('an operator-directed action stops the banner') is the
> correct altitude and is already applied; the specific verb needs no
> decision doc — which verb the banner names is not a tradeoff, and
> the code constant is its home. The mid-sprint rewrite is ratified
> retroactively.
>
> Rationale: CONCEPT-DEFINITION's no-CLI-verbs rule determines the
> wording, and a verb-choice decision would be padding under the
> decision-definition bar (no real alternative was on the table).

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
recommended batch at sign-off. Edit the text to redirect, empty
the section to discuss live, or delete this note to adopt the
ruling as your own. -->
