---
audit: bundled-executor-inproc-capability-advertisement
artifact: decision:bundled-executor-inproc-capability-advertisement
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:29:43Z
---

# In-process bundled handlers advertise capabilities straight into the discovery cache, marked static

Supported. Checked all four bundled executors and both bundled claim producers — the complete set the bundled registration entrypoint knows about. Each of the four executor packages exports its expected-attributes schema and, where it declares any, its tags and error classes as package-scope functions; the registration entrypoint reads those same functions to populate the discovery cache, and each package's gRPC observability capabilities response is built from the identical functions, so the two modes cannot diverge without one edit breaking both. Both bundled claim producers are registered by asking the constructed handler for its capabilities and advertising exactly what it reports. Every entry written this way is flagged static, and the periodic refresh loop skips static entries on both the executor and the claim-producer side, so a re-probe cannot blank an in-process advertisement. A configured endpoint of the same name suppresses the bundled advertisement rather than colliding with it. A live-stack test drives a bundled executor with no executor configuration at all and asserts the resulting peer entry is marked static, which is what distinguishes the in-proc path from an external service process.
