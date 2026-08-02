---
decision: revive-no-exported-rule
---

# Revive lint config

## Choice

Disable the lint rule that requires every exported symbol to carry a comment.

## Rationale

A mandatory comment on every exported symbol is noise, and it contradicts the project's zero-comment discipline, under which documentation comments exist only on explicitly opted-in public-API files.

## Alternatives

- Keeping the rule enabled (the linter's default posture) — rejected: forces a docstring on every exported symbol, directly conflicting with the comment-hygiene lint that forbids them.
