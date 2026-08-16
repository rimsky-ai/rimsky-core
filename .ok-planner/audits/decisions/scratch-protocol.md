---
audit: scratch-protocol
artifact: decision:scratch-protocol
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:33:36Z
---

# Scratch rides the execute request and the three settling terminal outcomes only

Supported. The executor protocol declares a scratch field on the execute request and on each of the three settling outcomes — success, error, park — and the async-callback body reuses those same three messages, so the async path carries scratch identically; the transient await-async hand-off message declares only an acknowledgement id and an expected-completion estimate and has no scratch field. Checked all four outcome variants and both response shapes. On the supervisor side every settling terminal routes its scratch through one shared writer: the success and infra-error paths, the error-policy paths, the park path, and the subgraph path all call it, and the acquisition path hydrates the row's persisted scratch unconditionally into the next dispatch request, spilled or inline. The writer returns immediately on a zero-length scratch, so an empty terminal attach performs no write and leaves the row's prior scratch intact, with a unit test pinning that no-op alongside tests for the inline, spilled, and spill-failure cases. Nothing else on the wire accepts scratch back from an executor.
