---
issue: stories-claim-producer-backend-collapse
kind: sprint
category: stories-collapses
artifacts:
  - story:claim-producer-filesystem
  - story:claim-producer-postgres
  - concept:claim-producer
status: open
opened: 2026-08-01T22:31:20Z
---

# Two bundled claim-producer stories are one outcome per backend; filesystem pick-policy shape has no home

## Problem

`story:claim-producer-filesystem` and `story:claim-producer-postgres` tell the same outcome (a production-grade bundled producer) once per backend. The filesystem story's prose additionally commits to pick-policy actions on Commit/Abandon and the SplitScope per-request discriminator enum (`list` / `batch_pick` / `expand_folder`) — stated nowhere in concepts or decisions.

## Candidates

- Collapse to one bundled-producer story with the backend choice in a decision; home the filesystem pick-policy/discriminator shape in a new decision or rule it below corpus altitude.
- Keep per-backend stories; decide only the pick-policy home.
