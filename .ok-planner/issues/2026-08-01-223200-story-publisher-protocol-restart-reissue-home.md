---
issue: story-publisher-protocol-restart-reissue-home
kind: sprint
category: stories-prescriptive
artifacts:
  - story:publisher-protocol
  - concept:publisher
  - concept:publisher-subscription
status: open
opened: 2026-08-01T22:32:00Z
---

# Publisher restart no-re-issue behavior is stated only in story prose

## Problem

`story:publisher-protocol`'s prose commits that on rimsky restart the publisher's already-active subscriptions are not re-issued; the publisher concepts describe row reconciliation but not this restart contract.

## Candidates

- Home it as an invariant on concept:publisher (or concept:publisher-subscription), then reduce the story.
- Rule it below corpus altitude and reduce.
