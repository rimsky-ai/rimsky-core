---
issue: story-http-node-split
kind: sprint
category: stories-splits
artifacts:
  - story:http-node
status: open
opened: 2026-08-01T22:30:10Z
---

# http-node bundles three distinct outcomes

## Problem

`story:http-node`'s sentence carries request/response routing, 429 rate-limit parking, and error-class field configuration — three capabilities a reader could want independently.

## Candidates

- Split into three stories (routing, 429 parking, error-class config).
- Keep bundled; rule the executor's surface one outcome.
