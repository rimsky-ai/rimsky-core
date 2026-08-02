---
issue: story-node-admin-split
kind: sprint
category: stories-splits
artifacts:
  - story:node-admin
status: open
opened: 2026-08-01T22:30:00Z
---

# node-admin bundles a read capability and a write capability

## Problem

`story:node-admin` promises both inspecting a node's full state (a read) and clearing a stale failure marker (a write backed by its own surface, per `decision:node-reset-clears-failure-marker`). Two distinct user-outcomes in one sentence.

## Candidates

- Split into an inspect story and a clear-failure-marker story.
- Keep bundled; rule the pairing intentional (one admin persona, one workflow).
