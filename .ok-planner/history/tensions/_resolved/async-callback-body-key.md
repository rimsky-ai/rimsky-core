---
resolved_by: spec:2026-05-12-nomenclature-resolution
tension: async-callback-body-key
category: unclear
status: open
affects:
  - executor
---

# Async-callback POST body key is `type` (not `kind`) — wire footgun documented but not surfaced in chi error

## What is muddy

Executors that hand off via `AsyncAccepted` POST to `${callback_url}/v1/callback/{async_ack_id}` with a body keyed `type`. The supervisor's chi route enforces this.

But:

- The natural convention elsewhere in the proto (and in many JSON event-bus styles) is `kind`. A new executor author intuitively picks `kind`.
- The chi route's rejection on `kind`-keyed body produces a generic error; the specific "use 'type' not 'kind'" message is not surfaced.
- Regression-tested in `executors/claude-agent/src/server.test.ts` but discoverable only by reading that test.

## Why it matters

Every new executor language port hits this. The error is hard to diagnose without prior knowledge.

## Resolution candidates (do NOT pick)

- Accept both `type` and `kind` server-side.
- Improve the chi route's error message to mention the exact required key.
- Codify the body shape in a documented JSON Schema published with the protocol.

## Evidence

- `_discover/2026-05-10-executor-streamed-execute.md` Observations bullet 2.
- `_discover/claude-agent-async-handoff-always.md` Description.
- CLAUDE.md "Non-obvious gotchas".

