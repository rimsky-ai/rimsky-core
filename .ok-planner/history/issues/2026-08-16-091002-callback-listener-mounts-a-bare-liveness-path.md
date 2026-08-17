---
issue: callback-listener-mounts-a-bare-liveness-path
kind: audit
category: inconsistent
artifacts:
  - decision:protocol-version-v1-namespaced
status: promoted
opened: 2026-08-16T09:10:02Z
sprint: 2026-08-17-accepted-intake-drain.md
---

# The supervisor's callback listener answers a health probe outside the version prefix

Every HTTP path rimsky serves is meant to sit under one version prefix (`/v1`), and a decision in the corpus says so with no carve-outs — the control API even puts its own liveness probe under the prefix. The supervisor's callback listener (the small HTTP surface external executors call back into with outcomes, keepalives and attribute writes) mounts its three callback routes under the prefix but its liveness probe at the bare root. It is the one route on the whole control-plus-callback surface reachable without the version prefix. The ruling decides whether the route moves under the prefix or the decision admits an exception.

The value the decision protects is that a reader can trust the rule without checking each listener; one bare path spends that. The fix is small — mount the probe under the prefix like the control API does — and the route-registry test that pins the control API's paths does not cover the callback listener, which is how the bare path went unnoticed.

## Options

- Move the callback listener's probe under the version prefix and extend the route-registry test's population to the callback listener; cost: any external probe configured against the bare path must be repointed.
- Amend the decision to name the callback listener's probe as its one bare-path exception; cost: the rule stops being checkable by reading and the exception must be remembered.

The ruling decides whether the rule or the route gives way.

## Ruling

> Generated ruling (/verify-issues): Move the callback listener's liveness probe under the version prefix, matching the control API's own probe, and widen the route-registry test so the callback listener's routes are in its population. The decision's own text forces this — it admits no bare-path carve-outs, and the artifact under audit is the code, not the rule. Verified against the tree as it stands; nothing was applied.

<!-- Owner: this is a generated ruling, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
