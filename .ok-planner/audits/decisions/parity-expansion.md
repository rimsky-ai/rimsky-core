---
audit: parity-expansion
artifact: decision:parity-expansion
text: compliant
implementation: unsupported
commit: d977250c
audited: 2026-08-16T05:26:42Z
checked: 77
unaccounted: 9
---

# Whether the driver-parity suite covers every queue, claim-handle, and frame behavior the runtime depends on

Unsupported. The cross-driver suite exists and does run against both persistence drivers — one entry point invoked twice, once per driver — and the wrong-claimant guard suite is genuinely a sub-tree of it, seventeen cases deep. But the universal fails on enumeration: taking the population as the methods of the three interfaces the decision names — the queue interface (33 methods), the claim-handle accessor (30), and the frame accessor (14), 77 in all, read off the interface declarations in the persistence package — nine are exercised nowhere in the parity suite, and each of the nine has a live non-test caller in the platform. The most serious is the frame-settlement method the graph layer's frame engine calls to end a settled frame: it is covered only by a single-driver package test, so a divergence between the two drivers in exactly the behavior parity exists to protect is invisible. The suite's own coverage of what it does cover is broad and per-scope careful; the gap is the observability and settlement edges nobody added when the methods landed.

## Unaccounted

- queue accessor `CountLive` — read by the metrics hook and the observability handler; no parity case
- queue accessor `GetAnyByID` — read by the control-api run lookup; no parity case
- claim-handle accessor `ListForObservability` — read by the observability handler; no parity case
- claim-handle accessor `GetByFrameAndNode` — read by the observability handler; no parity case
- claim-handle accessor `DeleteResolvedIfNoActiveHolders` — called by the control-api asset path; no parity case
- frame accessor `EndFrameIfSettled` — called by the graph frame engine; covered only by a single-driver package test
- frame accessor `ListForObservability` — called by the runtime's message-delivery path and three control surfaces; covered only by a single-driver package test
- frame accessor `ListForObservabilityWithMessage` — called by the control-api frames route; no parity case
- frame accessor `GetForObservabilityWithMessage` — called by the runtime's lineage writer; no parity case
