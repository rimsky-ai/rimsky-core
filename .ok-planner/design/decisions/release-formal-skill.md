---
decision: release-formal-skill
status: as-is
---

# Formal release flow

## Choice

The formal-release skill drives SemVer judgment + notes draft + outward push behind a single user gate.

## Rationale

Human review at the right point; automation everywhere else. SemVer classification and consumer-facing notes are judgment calls, and one gate placed after both puts the whole outward push behind a single reviewed confirmation.

## Alternatives

- A fully mechanical formal release (the dev-release shape) — rejected: SemVer judgment and consumer-facing notes need a human eye before anything ships outward.
- A confirmation gate at every step — rejected: repeated prompts add friction without adding safety once the chain itself is verified.
