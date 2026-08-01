---
decision: compose-engine-reuse
status: adopted
---

# One-shot verb reuses the compose engine

## Choice

The one-shot verb reuses the compose engine the compose lifecycle verbs already use: it constructs a control-api client against the loopback endpoint and invokes the existing apply path.

## Rationale

The compose engine is already an HTTP client, and input validation, idempotency, and error mapping all live on the HTTP boundary — reuse keeps one wiring path through them. The localhost round-trip cost is unmeasurable next to node-run latency.

## Alternatives

- A direct in-process bypass of the HTTP boundary — rejected: doubles the wiring through the engine and risks behavioral divergence, since validation, idempotency, and error mapping live on the boundary being bypassed.
