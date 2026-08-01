---
issue: stories-mechanism-prescription-tail
kind: audit
category: compliance
status: verified
opened: 2026-07-25T21:11:30Z
---

# A tail of stories prescribe internal mechanism instead of stating need

Roughly fifteen stories name internal components, algorithms, or wire identifiers in their statements — the gate evaluator, the cascade walker, JCS canonical hashing, specific state-machine values like `pending→stale` — which the story form reserves for decisions: stories state who needs what and why, never how the engine delivers it. Re-verification confirms the vocabulary is not a strippable tail in the worst files (`story:idempotent-mode-dedupes`, `story:iterative-workflows-converge`, `story:cascade-defers-during-flight`): the mechanism is interwoven through the core promise, so rewriting to observable outcomes risks changing what's promised and needs per-file judgment about which references are legitimate concept citations and which are mechanism description.

Two scope changes since filing. The claude-agent stdio admission ("not yet dispatched by a scenario") the filing wanted re-filed as its own coverage issue no longer exists anywhere in the story corpus — that sub-item is moot. And a new mandatory-field gap surfaced: `story:iterative-workflows-converge` has no "so that" clause at all — the one clause the story form makes mandatory — beyond the two circular so-that clauses already filed. This is the third of the three story-population issues that should run as one joint sweep (`issue:stories-delivery-surface-named-in-body`, `issue:stories-name-rimsky-yml-and-config-keys`).

## Options

- **Rewrite the tail in the joint stories sweep** — outcome language per file, mechanism references either dropped or converted to legitimate concept citations; the circular and missing so-that clauses repaired in the same pass.
- **Rule the current altitude acceptable** — requires loosening the story form the corpus enforces everywhere else.

The ruling confirms the forced direction; the per-file citation-vs-mechanism line is the sprint's editorial call.

## Ruling

> Generated ruling (/verify-issues): rewrite the mechanism-naming
> story tail to outcome language inside the joint stories sweep,
> repair the two circular so-that clauses, and give
> story:iterative-workflows-converge the mandatory so-that clause it
> currently lacks entirely; the stdio-admission sub-item is moot (the
> text no longer exists). The story-form and mandatory-so-that rules
> force each of these; only the editing judgment is sprint work.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
