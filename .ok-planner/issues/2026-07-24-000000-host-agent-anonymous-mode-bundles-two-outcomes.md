---
issue: host-agent-anonymous-mode-bundles-two-outcomes
kind: human
category: muddy-boundary
artifacts:
  - story:host-agent-anonymous-mode
status: verified
opened: 2026-07-24T00:00:00Z
---

# One story is making two promises — and a regression in one could hide behind the other

Rimsky's design corpus records durable user promises as "stories," one promise per story, each with its own failure conditions and its own proof. One story currently makes two: (a) a developer on a fresh, credential-less deployment can run a local host agent (a helper process that lets rimsky dispatch work to binaries on the developer's machine) and get work run to completion with no API key; and (b) when two developers each run their own anonymous agent against the same shared proxy, their work stays isolated — each agent receives only its own instances' dispatches. The story's title, benefit clause, and failure conditions all join the two with "and."

Why this matters beyond tidiness: the two promises regress independently. A second agent's registration could silently displace the first — breaking isolation — while single-agent dispatch keeps working, and the bundled story's single pass/fail signal would stay green-looking to anyone not re-reading the failure conditions clause by clause. Both halves independently clear the story bar ("would a user notice and complain if this regressed?"), which is itself the evidence they're separable. One test currently proves the bundle in one file; a sibling issue (`issue:story-host-agent-anonymous-mode-proof-under-exhibits`) separately shows even that test doesn't fully prove the isolation half — a gap that follows the isolation promise whichever way this splits.

## Options

- **Keep one story with compound acceptance** — no new file; the partial-regression blind spot stays.
- **Split into two stories** — the existing one narrows to no-credential dispatch; a new one takes isolation, collision rejection, and reconnect continuity, each with its own failure conditions. The existing test can carry both citations (multiple proofs per story are unrestricted).

The ruling decides: one or two; and if two, the new story's name and whether the existing test splits, doubles up its citations, or gets a companion.

## Ruling

> Recommended ruling (/recommend-rulings): Split: story:host-agent-
> anonymous-mode narrows to late-bind-under-anonymous-mode; a new
> story:anonymous-agents-isolated takes concurrent isolation,
> collision rejection, and reconnect continuity. The existing test
> carries both annotations, with the isolation story's proof
> strengthened per issue:story-host-agent-anonymous-mode-proof-under-
> exhibits.
>
> Rationale: Two independently-regressable promises deserve two
> falsifiers — the compound story hides a partial regression in one
> pass/fail signal. Multiple annotations on one test are unrestricted.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
recommended batch at sign-off. Edit the text to redirect, empty
the section to discuss live, or delete this note to adopt the
ruling as your own. -->
