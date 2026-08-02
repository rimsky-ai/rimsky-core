---
issue: stories-bundled-sensor-collapse
kind: sprint
category: stories-collapses
artifacts:
  - story:sensor-cron
  - story:sensor-http
  - story:sensor-object-store
  - story:sensor-webhook
  - concept:sensor
status: open
opened: 2026-08-01T22:31:10Z
---

# Four bundled-sensor stories are one outcome per substrate; webhook ack semantics have no home

## Problem

`story:sensor-cron`, `story:sensor-http`, `story:sensor-object-store`, and `story:sensor-webhook` each express "operator wires a sensor-driven message into a workflow" once per substrate. Additionally `story:sensor-webhook`'s prose commits to acknowledging the HTTP request only after rimsky has persisted the message — a durability contract stated in no concept or decision.

## Candidates

- Collapse to one bundled-sensor story with the substrate list in a decision; home the webhook ack-after-persist contract in concept:sensor or a decision.
- Keep per-substrate stories; home only the ack contract.
