---
issue: cli-register-drops-validation-warnings-on-success
kind: audit
category: inconsistent
artifacts:
  - concept:rimsky
  - concept:validation
status: promoted
opened: 2026-08-16T09:40:02Z
sprint: 2026-08-17-accepted-intake-drain.md
---

# The CLI drops the validator's advisories on a successful template registration

Registering a template returns advisories from the validators alongside the result; the server sends them on success. The CLI's client-side template type has no field for them, so the decoder discards them before the verb runs, and the verb prints them only on the failure path — the advisory channel is inverted, surfacing findings exactly when something already failed. The ruling adds the field.

## Options

- Add the field to the client type and print advisories on the success path as the failure path already does; cost: none.
- Also record a decision that every client response type decodes its full documented shape; cost: a process addition on top, optional (the same decode chokepoint underlies the dry-run misreport issue).

The ruling restores the advisory channel.

## Ruling

> Generated ruling (/verify-issues): Carry the validation advisories on the CLI's template response type and print them on a successful registration, plain and structured, as the failure path already does. Forced by the plain defect — the server sends a documented field the client silently discards; the validation-warnings story promises the author sees them. Verified against the tree as it stands; nothing was applied.

<!-- Owner: this is a generated ruling, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
