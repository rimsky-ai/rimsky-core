---
decision: async-callback-post-json
status: as-is
---

# Async-callback transport

## Choice

HTTP POST with a JSON outcome body to the supervisor's async-callback endpoint, keyed by the async-acknowledgement identifier (see `concept:supervisor`).

## Rationale

Simple, debuggable.
