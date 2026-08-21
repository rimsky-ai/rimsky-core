---
decision: idempotency-key-header-universal
---

# Idempotency on message send

## Choice

A mandatory idempotency key on every message-send surface: the header on the universal message-send endpoint, and a required argument on the MCP message-send tool (see `concept:message`, `concept:control-api`). Every surface rejects an omitted key — the route with a client error naming the header, the tool with a tool error naming the argument. No surface mints a key on the caller's behalf.

## Rationale

Replay-safe by construction, and the same on every surface: the caller that omits the key is the one that retries, and an agent calling the tool is that caller.

## Alternatives

- An optional idempotency key — rejected: replay safety becomes caller-dependent.
- A surface that mints a key silently when the caller omits one — rejected: a retry then mints a second key and double-sends.
- Deduplicate by content hash — rejected: two legitimate identical sends are indistinguishable from a replay.
