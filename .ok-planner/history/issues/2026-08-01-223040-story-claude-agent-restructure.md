---
issue: story-claude-agent-restructure
kind: sprint
category: stories-splits
artifacts:
  - story:claude-agent
  - decision:cli-spawn-mechanism
  - decision:signoff-crypto-ed25519
  - concept:error-policy
status: promoted
sprint: 2026-08-01-ruled-intake-drain.md
opened: 2026-08-01T22:30:40Z
---

# The claude-agent story carries duplicated prescriptions and the corpus's only copy of an error taxonomy

The story for the bundled claude-agent executor (the shipped service that runs Claude CLI sessions as workflow nodes) is the only story in the catalog still carrying prose past its story sentence — two extra paragraphs, which the catalog's own format forbids. That tail is mostly restatement: the MCP-server and expose-env capabilities already have their own stories (`story:claude-agent-mcp-servers-per-node`, `story:claude-agent-expose-env-per-node`), and the CLI-subprocess spawn mechanism and sign-off cryptography are already the recorded choices of two decisions (`decision:cli-spawn-mechanism`, `decision:signoff-crypto-ed25519`). Deleting the tail loses none of that.

One thing in the tail has no other home: a concrete thirteen-class error taxonomy (`blocked`, `internal-error`, `attribute-invalid`, …) the executor declares. The error-policy concept (`concept:error-policy`) deliberately stays abstract — it defines how declared error classes route outcomes, and names no concrete classes. So reducing the story to its sentence, which the format rules force, orphans the corpus's only record of that taxonomy — and the real question is whether the taxonomy deserves a durable home at all, or is protocol-surface detail the proto files and conformance suite already own.

## Options

- Reduce the story and home the taxonomy's *closedness* as a recorded decision (the choice: a closed, conformance-checked class set rather than free-form error strings), leaving the member list to the protocol surface — one new decision.
- Reduce the story and rule the whole taxonomy below corpus altitude — cheapest, but the corpus then nowhere says the class set is closed, which is the property error-policy routing quietly depends on.
- Keep the umbrella story's tail as the taxonomy's home — leaves the catalog's one remaining format violation standing.

The ruling decides where (or whether) the error taxonomy lives once the story is reduced to its sentence. The same stray-commitment pattern appears in `issue:stories-bundled-sensor-collapse` (webhook ack contract), `issue:stories-claim-producer-backend-collapse` (pick-policy shape), and `issue:story-bundled-park-resume-recipe-mechanism-home` — worth ruling together as one policy.

## Ruling

> Recommended ruling (/verify-issues): reduce the story to its sentence, strip the duplicated prescriptions, and record one decision: the claude-agent executor's declared error classes are a closed, conformance-checked set — free-form error strings were the alternative and were rejected. Leave the thirteen member names to the protocol definition and conformance suite.
>
> Rationale: the closedness is the commitment with a genuine tradeoff and a downstream dependent (error-policy routing only works if classes are stable and enumerable), so it clears the bar for a decision; the member list is churn-prone spelling the proto already owns, and copying it into the corpus buys a second place for it to rot. Ruling it entirely below corpus altitude trades away the one property the corpus actually leans on. Flip case: if the class set is not in fact conformance-checked today, the "closed set" framing overstates reality and the cheaper below-altitude option becomes the honest one.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
