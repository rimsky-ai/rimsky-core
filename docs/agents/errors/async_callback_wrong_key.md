---
error: async_callback_wrong_key
surfaced_to: executor
---

# Async-callback wrong key

## What it means

An executor posted to the supervisor's async-callback URL with body keyed `kind:` instead of `type:`. The supervisor's callback route enforces the exact key `type:` and rejects any other key.

## When it happens

When implementing an executor that uses the async-callback path. The executor's body shape must match the supervisor's expectation. A common mistake: copying the streaming-event Go struct (which uses `kind`) into the callback body when JSON-encoding it.

## What to do

Change the body key from `kind` to `type`. The TS claude-agent reference impl's test suite (under `executors/claude-agent/src/server.test.ts`) covers the exact wire shape; align with that.

## See also

- [`../../concepts/executor.md`](../../concepts/executor.md)
- [`../../protocols/executor.md`](../../protocols/executor.md)
