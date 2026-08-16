---
audit: bundled-registry-entrypoint
artifact: decision:bundled-registry-entrypoint
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:43:41Z
---

# One static entrypoint registering the bundled handlers, with operator config winning

Supported. A single exported registration function in the services module is the only path that wires bundled handlers, and it enumerates them statically: four executor entries — every directory under the bundled executors tree that is an executor — in one table literal, plus the two bundled claim producers, matching the whole population of in-process-registrable bundled services. It takes four narrow sink interfaces declared alongside it whose method signatures use only protocols-module types, and the rimsky side supplies a collector implementing all four and calls the function exactly once per unified process, from the two entrypoints that start such a process. Construction failure of a configured handler returns an error naming the handler, at every one of the six construction sites; a handler whose configuration is absent is logged and skipped, which the claude-agent credentials case and both claim-producer cases exercise. Config-wins precedence is enforced at all four places bundled registrations meet operator configuration — the supervisor's executor alias resolver, the control API's executor entry map, the claim-producer registry, and the discovery advertisement pass — each skipping any name the unified config already declares, and each has a named test asserting the configured value survives. An end-to-end scenario drives a node through a bundled in-process executor with zero executor configuration present.
