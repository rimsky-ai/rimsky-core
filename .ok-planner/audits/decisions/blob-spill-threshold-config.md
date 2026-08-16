---
audit: blob-spill-threshold-config
artifact: decision:blob-spill-threshold-config
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:30:00Z
---

# A configurable byte threshold governs spill, defaulting to fully inline

Supported. The spill decision is one predicate taking the backend, the threshold, and the payload size, and it is the only such decision point: all three production spill surfaces — the attribute bag in each of the two storage drivers, and the per-dispatch scratch payload in the runtime — route through it, so one configured number governs the whole surface. The threshold is a per-deployment field on the blob configuration block, read from the deployment's configuration file when present and pushed into the driver alongside the backend at startup. The default holds as stated: the default configuration selects the inline backend, and the predicate short-circuits to false whenever the backend names itself inline, so no payload spills under the default whatever its size. Both rejected alternatives are absent — the threshold is not a compiled-in constant, and no path spills unconditionally.
