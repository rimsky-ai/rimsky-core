---
tension: unreachable-service-row-stall
category: unspecified
status: open
affects:
  - executor
  - claim-producer
  - supervisor
  - error-policy
---

# Dispatch rows for unknown / unreachable services sit in queue with no error class

## What is muddy

When a dispatch row's `executor_name` (or required claim-producer) is not in any supervisor's `accepted_executors`, the row sits in queue indefinitely. No synthetic error class fires. Hosted and proxy-mediated executors share this behavior.

## Why it matters

In the proxy case, the user's agent not being connected is a normal transient state, and the rimsky operator may want to alert on it after a threshold. There is no mechanism for that today. The hosted case has the same problem (a misconfigured `accepted_executors` or a dead executor service silently swallows dispatches).

## Resolution candidates (do NOT pick)

- A new synthetic error class `acquire/unreachable` fired after a threshold age on the row.
- A watchdog that flips rows from `queued` to `failed` with a configured class after a timeout.
- A per-template `min_admissible_age:` field.

## Evidence

- This spec: `.ok-planner/specs/2026-05-24-host-agent-and-proxy-design.md` §"Error handling".
