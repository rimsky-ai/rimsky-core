---
issue: stories-fanout-partition-collapse
kind: sprint
category: stories-collapses
artifacts:
  - story:fs-fanout-expand-folder
  - story:fs-fanout-list-array
  - story:pg-fanout-list-array
status: promoted
sprint: 2026-08-01-ruled-intake-drain.md
opened: 2026-08-01T22:31:00Z
---

# Two fan-out list stories are the same promise told twice; the folder story is not

Three stories cover fan-out partitioning (splitting one node into parallel work units, one per item of a parent claim). Two of them — fan-out over an upstream list against the filesystem store, and the same against the Postgres store — are near-verbatim: same capability (declare a fan-out whose partition request is a list of items produced upstream), same benefit (one parallel work unit per item, no custom claim-producer to write), differing only in which bundled store holds the parent claim. The third, folder expansion against the filesystem store, is a different partition grammar — expand a folder's contents into work units — meaningful only against a filesystem.

The story-authoring rules state directly that two stories describing the same user-outcome through different surfaces are one story, with the surface belonging to a decision. The list-array pair instantiates that rule literally, with "which bundled store" as the surface. The folder story does not: its outcome differs in kind, not in backend. So the rules force the collapse of exactly the pair — but collapsing retires two story files and creates one (plus the backend-choice decision), a change to the artifact set only a sprint may make.

## Options

- Collapse the list-array pair into one story with the backend choice recorded as a decision, leaving folder expansion standing — the rule-forced shape.
- Collapse all three — mashes a genuinely distinct partition grammar into the list capability; the rule does not support it.
- Keep all three — leaves a literal same-outcome duplicate the rule directly forbids.

The ruling confirms the rule-forced collapse of the pair. Siblings `issue:stories-bundled-sensor-collapse` and `issue:stories-claim-producer-backend-collapse` ask the same collapse question with materially weaker cases — ruling them together keeps the line consistent.

## Ruling

> Generated ruling (/verify-issues): collapse the two fan-out-over-a-list stories into one, record which bundled store backs the claim as the decision-level surface choice, and leave the folder-expansion story standing. The story rules' same-outcome/different-surface test forces exactly this: the pair differs only by backend; the folder grammar is a different outcome.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
