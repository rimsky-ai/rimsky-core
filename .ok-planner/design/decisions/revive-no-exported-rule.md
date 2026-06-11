---
decision: revive-no-exported-rule
status: as-is
---

# Revive lint config

## Choice

Disable the `exported` rule.

## Rationale

Every exported symbol carrying a comment is noise; focus on load-bearing ones.
