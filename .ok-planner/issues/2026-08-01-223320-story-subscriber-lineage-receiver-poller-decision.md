---
issue: story-subscriber-lineage-receiver-poller-decision
kind: sprint
category: stories-prescriptive
artifacts:
  - story:subscriber-lineage-receiver
  - concept:lineage
status: open
opened: 2026-08-01T22:33:20Z
---

# Lineage receiver's poll-based mechanism is prescribed in the story

## Problem

`story:subscriber-lineage-receiver` names the implementation (a Postgres poller over the concept:lineage projection, no LifecycleSubscriber RPC) in the sentence; no decision records that implementation choice.

## Candidates

- Record the poller choice as a decision and let the sentence state only the outcome.
- Keep the mechanism in the sentence; rule it part of the promise.
