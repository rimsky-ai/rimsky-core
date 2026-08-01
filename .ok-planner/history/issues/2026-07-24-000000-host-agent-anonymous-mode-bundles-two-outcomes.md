---
issue: host-agent-anonymous-mode-bundles-two-outcomes
kind: human
category: muddy-boundary
artifacts:
  - story:host-agent-anonymous-mode
status: promoted
sprint: 2026-08-01-guidance-realignment-drain.md
opened: 2026-07-24T00:00:00Z
---

# One story is making two promises — and a regression in one could hide behind the other

Rimsky's design corpus records durable user promises as stories, one promise per file in spirit, though no written rule actually mandates it. `story:host-agent-anonymous-mode` currently makes two: (a) a developer on a fresh, credential-less deployment can run a local host agent (a helper process that lets rimsky dispatch work to binaries on the developer's machine) and get work run to completion with no API key; and (b) when two developers each run their own anonymous agent against the same shared proxy, their work stays isolated — each agent receives only its own instances' dispatches. The story joins the two with an "and" in its single statement.

Why this matters beyond tidiness: the two promises regress independently. A second agent's registration could silently displace the first — breaking isolation — while single-agent dispatch keeps working, and the bundled story gives the periodic implementation audit one determination to cover both, so a partial regression hides behind the surviving half. Both halves independently clear the story bar (a user would notice and complain if either regressed), which is itself the evidence they are separable. The state of play has improved since filing: the isolation half now has a genuinely strengthened proof — the multi-agent scenario test asserts per-agent attribution end to end (`code:test/scenarios/host_agent_anonymous_multi_agent_isolation_test.go`, reading a routing label the spawn path now forwards), verified to redden under fault injection. What remains is purely the story-structure call: the story-definition sets the per-story inclusion bar but is silent on bundling, so no rule forces either shape.

## Options

- **Split into two stories** — the existing one narrows to no-credential dispatch; a new `story:anonymous-agents-isolated` takes concurrent isolation. The strengthened test carries both annotations (multiple annotations per test are unrestricted), and each promise gets its own audit determination.
- **Keep one story with the compound statement** — no new file; one audit determination keeps covering two independently-regressable promises.

The ruling decides one story or two.

## Ruling

> Recommended ruling (/verify-issues): split — narrow
> story:host-agent-anonymous-mode to credential-less late-bind
> dispatch, and add story:anonymous-agents-isolated for concurrent
> per-agent isolation; the existing multi-agent scenario test carries
> both annotations.
>
> Rationale: the audit corpus writes one supported/unsupported
> determination per story, so two independently-regressable promises
> under one slug is exactly the shape that lets a partial regression
> read as supported; splitting costs one small file. The flip case:
> if the audit model ever moves to per-clause determinations, the
> bundling stops hiding anything and the split loses its point.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
