---
decision: config-flip
---

# Plumbline check activation follows clean state

## Choice

Activation of an inactive Plumbline check follows clean state, not preceding it. The configuration change that flips a check from inactive to active is committed only after the codebase is already clean against that check.

## Rationale

The lint blocks edits that introduce new violations of any active check. Activating a check whose backlog is non-zero would block the very edits that would resolve the existing violations. Activating after the backlog reaches zero converges to full enforcement at the moment the codebase actually meets it.

## Alternatives

Activating the check before the sweep — rejected because the edit-blocking PostToolUse lint would prevent the sweep itself.
