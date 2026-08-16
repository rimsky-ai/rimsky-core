---
audit: service
artifact: concept:service
text: compliant
implementation: unsupported
commit: d977250c
audited: 2026-08-16T05:15:12Z
checked: 11
unaccounted: 1
---

# The orchestrated service: declaration, advertisement, conformance, composition, trust boundary, and the port-precedence rule over every bundled binary

Unsupported, on coverage. Five of the six invariants hold outright. Out-of-process services are declared in the unified config with an explicit protocol list per entry, and in-process bundled handlers register their membership and their capabilities — schema, tags, declared error classes for an executor, declared error classes only for a claim producer — through the bundled registration entrypoint, which writes them straight into the capabilities cache and marks them exempt from probing. Out-of-process membership is advertised at startup by the per-protocol capabilities query. Conformance ships as subcommands of the single binary covering the executor, claim-producer, publisher, validation, data-processing, blob-backend and lifecycle-subscriber protocols plus a probe, so the lifecycle-subscriber pass the concept singles out exists. Multi-protocol binaries compose distinct handler types, one per protocol, registered separately on one server, with no shared capabilities-provider across protocols. A standing service's trust posture follows the deployment switch and is orthogonal to the per-peer server-verification key. The port-precedence invariant is the exception: the shared resolver — agent-assigned variable first, the service's own variable or config value next, built-in default last — is used by nine of the eleven bundled out-of-process binaries; the openlineage subscriber serves no port at all and is accounted for by that; the webhook sensor serves a gRPC port read directly from its own variable with no agent override, so it cannot be late-bound and its readiness poll would never see a listener on the port the agent chose.

## Unaccounted

- The webhook sensor binary reads its gRPC serving port from its own port variable alone and never consults the agent-assigned port variable, so it is outside the shared precedence the invariant claims for every bundled binary.
