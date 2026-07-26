---
issue: stories-mechanism-prescription-tail
kind: audit
category: compliance
status: verified
opened: 2026-07-25T21:11:30Z
---

# A tail of stories prescribe internal mechanism, and two admit unproven coverage

Roughly fifteen stories name internal components, algorithms, or wire identifiers in their Role/Capability — the gate evaluator, the cascade walker, the diff-gate, JCS hashing, specific RPCs and state-machine values — which the story form reserves for decisions. Spot-checks confirm the pattern across the sub-families (component naming, algorithm naming, protocol naming). Two stories additionally have circular so-that clauses that restate their capability instead of naming a benefit, and the claude-agent stories carry a forward-looking admission ("stdio... not yet dispatched by a scenario") that the current-state-only rule bars outright — that gap belongs in this intake as its own question, not as prose inside a story.

The affected files are enumerated in this issue's git history (the as-filed body).

## Options

- A remediation sprint rewrites the tail to observable outcomes, fixes the two so-that clauses, and converts the claude-agent stdio admission into a filed issue about scenario coverage. Cost: one focused sprint pass.
- Rule the current altitude acceptable — would require amending the story-form tightening the corpus already enforces elsewhere.

## Ruling

> Generated ruling (/verify-issues): a sprint rewrites the mechanism-naming story tail
> to outcome language, repairs the two circular so-that clauses, and re-files the
> claude-agent stdio scenario-coverage gap as its own issue. The story-form and
> current-state-only rules force each of these.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
