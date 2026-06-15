---
decision: revive-no-exported-rule
status: as-is
---

# Revive lint config

## Choice

Disable the lint rule that requires every exported symbol to carry a comment.

## Rationale

Every exported symbol carrying a comment is noise; focus on load-bearing ones.
