---
issue: story-node-admin-split
kind: sprint
category: stories-splits
artifacts:
  - story:node-admin
status: verified
opened: 2026-08-01T22:30:00Z
---

# Should the node-admin story split its read and its write?

The catalog's node-admin story promises two things in one sentence: an operator can inspect a node's full state (a read), and can clear a stale failure marker off a failed node (a write, served by its own API verb). The filed worry is granularity — two distinct user-outcomes fused into one story, where each story is supposed to be an individually checkable promise.

Re-verification found the story well-formed: a single canonical story sentence, no prescriptive tail. The two capabilities are different verbs on the wire, but they serve one operator workflow — you inspect the node's true state precisely to decide whether to reset it. The reset verb's own design record (`decision:node-reset-clears-failure-marker`) frames reset as an observability-surface verb, the same framing: read and repair as one admin toolkit. No corpus rule sets a one-outcome-per-story bar, and the catalog elsewhere bundles multi-verb personas the same way (API-key management, template lifecycle).

## Options

- Split into an inspect story and a clear-marker story — two files for a two-line capability, and the "read informs the reset" narrative is lost.
- Keep bundled and rule the pairing intentional — no identified cost.

The ruling decides whether one operator workflow may carry two verbs in one story. Siblings `issue:story-http-node-split`, `issue:story-instance-lifecycle-split`, and `issue:story-runtime-diagnostics-split` pose the same granularity question and should be ruled consistently.

## Ruling

> Recommended ruling (/verify-issues): keep the story bundled. Inspect-then-reset is one workflow for one persona; splitting it manufactures two artifacts nobody evaluates separately.
>
> Rationale: the split buys nothing — no reader checks "can I inspect?" apart from "can I intervene?" — and the catalog already treats multi-verb personas as single stories, so splitting here would make the granularity policy less consistent, not cleaner. Flip case: if a future consumer ships the read surface without the reset verb, or gates them behind different roles, the outcomes become independently deliverable and the split becomes real.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
