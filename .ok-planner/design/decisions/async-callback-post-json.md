---
decision: async-callback-post-json
status: as-is
---

# Async-callback transport

## Choice

HTTP POST with a JSON outcome body to `${callback_url}/v1/callback/{async_ack_id}`.

## Rationale

Simple, debuggable.
