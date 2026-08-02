---
issue: story-host-agent-per-binding-overrides-defaults-home
kind: sprint
category: stories-prescriptive
artifacts:
  - story:host-agent-per-binding-overrides
  - concept:host-agent
status: verified
opened: 2026-08-01T22:31:50Z
---

# The per-binding override defaults belong beside the invariant that already covers env

The host agent (the daemon that spawns local service binaries) lets each binding override how its child is spawned; the story covering that promises the defaults too — a binding with no overrides spawns with inherited environment, the global working directory, and the global ready-timeout. The format rules force the story down to its sentence, and the fallback behavior needs a home first.

Re-verification confirms the code does exactly this (`code:lib/runtime/hostagent/spawn.go`): empty binding cwd falls back to the spawn's global cwd, zero timeout falls back to the global value, args come straight from the binding. And the host-agent concept already carries the sibling fact in exactly this shape — its invariants state that spawned children inherit the agent's full environment, overridable per binding on name collisions (`concept:host-agent`). The argv/cwd/timeout defaults are the same kind of boundary property, missing from the same list. No decision-worthy tradeoff exists (a fallback-to-global default has no rejected alternative), so the authoring rules leave one compliant end state; reaching it adds to a concept's invariants, which only a sprint may do.

## Options

- Extend the host-agent concept's invariant to cover argv, cwd, and timeout alongside env — completes an existing commitment in its existing home.
- Rule the defaults below corpus altitude — leaves the concept stating one spawn default while the code enforces four, an arbitrary split.

The ruling confirms the rule-forced homing.

## Ruling

> Generated ruling (/verify-issues): extend the host-agent concept's spawn-inheritance invariant to state all four per-binding defaults — env inherited, argv from the binding, cwd and ready-timeout falling back to the spawn's globals — then reduce the story to its sentence. The concept already owns this property for env; the rules' one-home principle forces the remaining three into the same bullet rather than leaving them orphaned in story prose.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
