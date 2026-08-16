---
audit: compose-engine-reuse
artifact: decision:compose-engine-reuse
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:44:00Z
---

# The one-shot's reuse of the compose engine over a loopback control-api client read against the compose package

Supported. The one-shot boots its role stack, waits for the control API to answer on a loopback HTTP endpoint it composed from the port it picked, constructs the ordinary CLI control-api client against that endpoint, and then calls the same three engine functions the lifecycle apply path calls — query state, compute plan, apply plan — with no alternative in-process entry point anywhere in the package. The rejected direct bypass does not exist: there is exactly one wiring path into the engine, and validation, idempotency, and error mapping stay on the HTTP boundary for both callers.
