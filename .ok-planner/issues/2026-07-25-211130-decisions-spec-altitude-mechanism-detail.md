---
issue: decisions-spec-altitude-mechanism-detail
kind: audit
category: compliance
status: verified
opened: 2026-07-25T21:11:30Z
---

# About twenty decisions read as specs rather than choice points

Decisions record a choice, its rationale, its alternatives, and its proof — not schemas, call sequences, or literal wire detail. About twenty decisions sit below that altitude: literal routes and status codes (keepalive-endpoint), full attribute schemas plus dispatch algorithms (loop-counter-shape), exhaustive state-transition walkthroughs (held-as-state-not-phase), multi-paragraph runtime mechanics (claude-agent-env-passthrough-allowlist), and Go function names (per-service-load-opts-from-env). The corpus's own precedent shows the target altitude: a sibling decision deliberately abstracts status codes to "a created-resource status code," proving the house style already exists.

The full list is in this issue's git history (the as-filed body).

## Options

- A remediation sprint trims each to choice altitude, relocating spec detail to code and tests. Cost: one focused sprint pass.
- Rule the lower altitude acceptable for decisions — inconsistent with the corpus's own abstraction precedent and the decision-form rule.

## Ruling

> Generated ruling (/verify-issues): a sprint trims the ~20 spec-altitude decisions to
> choice/rationale/alternatives/proof form, relocating schemas, routes, literal codes,
> and algorithms into code and tests. The decision-form rule ("decisions are not specs")
> forces the direction; only the editing is sprint work.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
