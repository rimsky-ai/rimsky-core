---
decision: compose-engine-reuse
status: adopted
---

# compose-engine-reuse

## Choice

Reuse the existing compose engine that the `up`/`down`/`plan`/`status` verbs already use. The one-shot verb constructs a control-api client against the loopback endpoint and invokes the existing apply path.

## Rationale

The compose engine is already an HTTP client. A direct in-process bypass of the HTTP boundary would double the wiring through the engine and risk behavioral divergence (input validation, idempotency, and error mapping all live on the HTTP boundary). The localhost round-trip cost is unmeasurable next to node-run latency.
