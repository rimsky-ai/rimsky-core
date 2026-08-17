---
issue: role-orchestration-is-shared-not-mirrored
kind: audit
category: conflicting
artifacts:
  - decision:launch-integration
status: promoted
opened: 2026-08-16T10:00:08Z
sprint: 2026-08-17-accepted-intake-drain.md
---

# The launch-integration decision rejects the shared launcher the tree already has

Two sites start the three core roles in one process: the entrypoint's unified mode and the compose one-shot verb. The launch-integration decision says the second mirrors the first's start-track-select-drain loop and records extracting that loop into a shared helper as the rejected alternative. The tree did the rejected thing: one exported launcher owns start, stop-tracking, the failure channel and reverse drain, and both sites call it; only the signal-versus-failure select is written per site because each has its own signal source. A reader judging whether the sites are independent is told yes when three of four steps are one function. The ruling corrects the decision to the shared shape.

## Options

- Restate the Choice as the shared launcher with the per-site select, and drop or reverse the rejected alternative; cost: none.
- Reverse the extraction; cost: deleting working shared code for no stated benefit.

The ruling makes the decision describe the launcher that exists.

## Ruling

> Generated ruling (/verify-issues): Rewrite the decision so its Choice is the shared launcher — one function starting the roles in order, tracking stops, owning the failure channel and draining in reverse — with the signal-vs-failure select as the one per-site step, and remove the shared-helper extraction from the rejected alternatives. Forced by the current-state-only rule; the sharing is exercised and treated as a convention by a sibling decision. Verified against the tree as it stands; nothing was applied.

<!-- Owner: this is a generated ruling, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
