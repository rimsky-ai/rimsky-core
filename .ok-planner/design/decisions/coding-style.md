---
decision: coding-style
status: as-is
---

# Coding style

## Choice

Rimsky's coding methodology is Plumbline, consumed as a Claude Code plugin. The plugin materializes the methodology's per-session cheatsheet into the repo where every contributor and agent reads it; the cheatsheet is committed so contributors without the plugin still see the rules. A PostToolUse lint blocks edits that introduce stale `@source:` references or uncovered `@blessed-invariant:` slugs; CI invokes the same lint against the full tree. Project-specific tag-vocabulary extensions configure the plugin to recognize the design-citation tags this project uses (`@concept:`, `@story:`, `@decision:`).

The comment-hygiene check is currently disabled because rimsky follows Go's GoDoc convention (exported symbols carry a doc comment whose first word is the symbol name) and the plugin's regex-based hygiene check has no language-aware exemption for that idiom yet. Enabling the check is gated on either an upstream plugin improvement that recognizes GoDoc-shape comments as machine-directive-equivalent, or a decision to migrate rimsky away from GoDoc; until one of those happens the check stays off, the other two run.

## Rationale

Plumbline's thesis — comprehension is cheap for current agents, verification is not, so every load-bearing convention becomes a mechanical check — matches rimsky's existing posture: enforced import boundaries, `@blessed-invariant` annotations exercised by scenario tests, the protocol conformance suite. Adopting the plugin closes the loop on the remaining gap (comment hygiene plus tag validity) without changing the practices already in place, and lets the methodology evolve via plugin upgrade rather than repo-by-repo doc rewrites.

## Alternatives

Inlining the style docs in this repo (the prior arrangement): rejected — the methodology evolves per model generation; shipping it as a plugin lets that evolution arrive once for every consuming project.
