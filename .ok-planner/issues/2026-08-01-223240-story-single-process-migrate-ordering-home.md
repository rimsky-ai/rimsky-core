---
issue: story-single-process-migrate-ordering-home
kind: sprint
category: stories-prescriptive
artifacts:
  - story:single-process-all-in-one
  - decision:single-process-mode
status: verified
opened: 2026-08-01T22:32:40Z
---

# Migrate-before-roles is an entrypoint property, not an all-in-one property — home it where the entrypoint is recorded

The all-in-one story (the single-process stack that runs every role for local dev) promises in prose that database migrations run synchronously before any role starts. The format rules force the story down to its sentence; the ordering needs a home first — and the filed candidate (the single-process decision) turns out to be the wrong one.

Re-verification shows the property is general: the container entrypoint runs migration synchronously before starting roles in *both* its paths — the all-in-one path and the split-role path — whenever the invocation owns migration (the unified process always does; a single-role container only when its role is the control API) (`code:cmd/rimsky-entrypoint/main.go`). The single-process decision is scoped to why one shared process exists and says nothing about migrate timing (`decision:single-process-mode`); the entrypoint role-selection decision already states that migrate runs once per deployment with the owner role determined by the command (`decision:image-entrypoint-role-selection`) — the synchronous-before-roles ordering is the missing half of that same recorded fact. Completing a decision's text is a sprint-level act.

## Options

- Complete the entrypoint role-selection decision with the ordering — migration finishes before any role starts, in every topology — correcting the issue's own premise about where this belongs.
- Amend the single-process decision as filed — records a general property in a topology-specific artifact, guaranteeing a second copy when someone notices it holds for split roles too.
- Rule it below corpus altitude — leaves "roles never see a half-migrated schema" resting on story prose scheduled for deletion.

The ruling confirms the rule-forced homing. Rule together with `issue:story-rimsky-deployment-bootstrap-unknown-command-home` — both complete the same decision file, likely as one change.

## Ruling

> Generated ruling (/verify-issues): complete the entrypoint role-selection decision with the ordering guarantee — when an invocation owns migration, migration runs synchronously to completion before any role starts, in all-in-one and split-role topologies alike — then reduce the story to its sentence. The decision already records the who-migrates half of this fact; the one-home principle forces the when half into the same artifact, and the general property may not be filed under the topology-specific decision.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
