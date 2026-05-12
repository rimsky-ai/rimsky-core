---
tension: claimant-guarded-backtick-noun
category: inconsistent
status: resolved
spec: 2026-05-11-design-log-convergence
affects:
  - lifecycle-handler
resolution:
  shape: strip-backticks
  summary: |
    Stripped backticks around `claimant-guarded` in lifecycle-handler.md.
    Claimant-guarded is an invariant pattern documented at
    @blessed-invariant 4, not a concept slug. Typographic convention:
    backticks reserved for concept slugs and code identifiers, not
    invariant-phrase shorthand.
---

# `claimant-guarded` is typeset as a noun via backticks but it is an invariant pattern

## What is muddy

`concepts/lifecycle-handler.md` references "claim release (see `auto-terminal`, `claimant-guarded`)". The backticks frame `claimant-guarded` as a sibling concept slug, but no `concepts/claimant-guarded.md` exists. "Claimant-guarded" is an invariant *pattern* documented at `@blessed-invariant 4` — every `DELETE FROM rimsky_claim_handle` and `UPDATE … SET claimed_by = NULL` is `AND … = supervisor_id`. It describes a release discipline, not a noun the system traffics in.

## Why it matters

Cross-link integrity: a reader sees a backticked term and grep'd for a concept file that doesn't exist. Typographic convention drift — backticks should be reserved for actual concept slugs (or code identifiers), not invariant-phrase shorthand.

## Resolution candidates (do NOT pick)

- **Strip the backticks** in `concepts/lifecycle-handler.md` and either (a) reword to "the claimant-guarded release discipline (`@blessed-invariant 4`)" or (b) drop the parenthetical, since `auto-terminal` is the cross-link that matters.
- **Promote** to a tiny invariant-shaped concept entry (lowest-value option; `@blessed-invariant 4` already owns the canonical statement).

## Evidence

- `concepts/lifecycle-handler.md`.
- CLAUDE.md `@blessed-invariant 4` ("claimant-guarded release").
- `review-notes.md` "Suspected-but-unconfirmed concepts" / "Unresolved issues".

