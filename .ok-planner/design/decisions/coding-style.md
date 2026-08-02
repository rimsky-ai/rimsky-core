---
decision: coding-style
---

# Coding style

## Choice

Rimsky's coding methodology is Plumbline, consumed as a Claude Code plugin. The plugin materializes the methodology's per-session cheatsheet into the repo where every contributor and agent reads it; the cheatsheet is committed so contributors without the plugin still see the rules. The lint runs the `comment_hygiene` and `citation_resolution` checks. GoDoc-style and JSDoc-style doc-comment blocks are exempt from comment-hygiene only in files carrying an opt-in file-level marker — canonical doc shapes do not pass automatically without it. A PostToolUse lint blocks edits that introduce new violations across any check; CI invokes the same lint against the full tree. Project-specific tag-vocabulary extensions configure the plugin to recognize the design-citation tags this project uses (`@concept:`, `@story:`, `@decision:`).

## Rationale

Plumbline's thesis — comprehension is cheap for current agents, verification is not, so every load-bearing convention becomes a mechanical check — matches rimsky's existing posture: enforced import boundaries, load-bearing constraints exercised by scenario tests, the protocol conformance suite. Adopting the plugin closes the loop on the remaining gap (comment hygiene plus tag validity) without changing the practices already in place, and lets the methodology evolve via plugin upgrade rather than repo-by-repo doc rewrites.

## Alternatives

- Inlining the style docs in this repo — rejected: the methodology evolves per model generation; shipping it as a plugin lets that evolution arrive once for every consuming project.
