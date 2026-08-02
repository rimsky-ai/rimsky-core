---
issue: story-rimsky-deployment-bootstrap-unknown-command-home
kind: sprint
category: stories-prescriptive
artifacts:
  - story:rimsky-deployment-bootstrap
  - decision:image-entrypoint-role-selection
status: promoted
sprint: 2026-08-01-ruled-intake-drain.md
opened: 2026-08-01T22:32:10Z
---

# The entrypoint's unknown-command exit is the undocumented third branch of a recorded decision

The rimsky container image's entrypoint selects which roles to run from its container command: no command runs all roles, a known role name runs that role alone. The deployment-bootstrap story adds in prose that an unknown command exits non-zero with a clear error — the safety behavior that stops a typo'd role name from silently doing the wrong thing. The format rules force the story down to its sentence; the error path needs a home first.

Re-verification confirms the behavior (`code:cmd/rimsky-entrypoint/main.go::selectRoles`, exit code 2 on any argument outside the three roles). The corpus already records this exact mechanism: the entrypoint role-selection decision states the no-command/single-role dichotomy for the very same function, and the code site carries that decision's annotation (`decision:image-entrypoint-role-selection`). The unknown-command branch is the third arm of the choice that decision documents — not a new choice, an incomplete recording of an existing one. Completing a decision's text is a sprint-level act.

## Options

- Complete the role-selection decision with the unknown-command error branch — finishes the artifact that owns the function.
- Rule it below corpus altitude — leaves a decision documenting two of its mechanism's three branches.

The ruling confirms the rule-forced homing. Rule together with `issue:story-single-process-migrate-ordering-home`, which completes the same decision file's migrate-ordering gap — likely one combined change.

## Ruling

> Generated ruling (/verify-issues): complete the entrypoint role-selection decision with its third branch — any command that is not a known role name exits non-zero with an error naming the value — then reduce the story to its sentence. The decision already owns this function's behavior; recording two branches of a three-branch choice is an incomplete expression of the same commitment, and the one-home principle forces the completion there.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
