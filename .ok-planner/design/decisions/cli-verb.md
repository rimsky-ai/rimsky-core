---
decision: cli-verb
status: adopted
---

# cli-verb

## Choice

The ephemeral-run capability is exposed as a sub-verb under the compose dispatcher, sibling to the existing compose lifecycle verbs (up, down, plan, status). The lifecycle verbs require a running rimsky reachable over the control-api; the ephemeral-run sub-verb does not.

## Rationale

Sits naturally alongside the existing compose family. The verb operates on the same manifest format with the same engine; verb-naming consistency makes the surface scannable.
