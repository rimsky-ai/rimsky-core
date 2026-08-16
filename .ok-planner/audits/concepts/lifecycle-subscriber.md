---
audit: lifecycle-subscriber
artifact: concept:lifecycle-subscriber
text: compliant
implementation: unsupported
commit: d977250c
audited: 2026-08-16T05:15:12Z
---

# The opt-in lifecycle protocol: seven callbacks, the delivery-site enumeration, and the idempotency guard

Unsupported. Five of the six invariants hold; the delivery-site enumeration does not. The protocol declares exactly the seven callbacks the concept names — four template events, instance created and terminated, and run-scope terminal — and carries no node-cascade transition. Opt-in is a protocol-membership entry, checked identically across the claim-producer, executor and publisher blocks, so any peer-service kind can subscribe and a peer that does not is silently skipped at fan-out. Idempotency is a persisted row keyed by peer, scope kind and scope id; every delivery site takes the per-lifecycle-scope advisory lock and does its check-deliver-mark inside one transaction, and the row advances only after the peer acknowledges, so a failed delivery is retried on the next fan-out — at-least-once, not exactly-once. The candidate set differs by scope exactly as claimed: template events go only to peers the spec references, while instance-keyed and run-scope-keyed events additionally include the configured late-bind proxies. The template-registered callback carries the spec bytes. Firing is synchronous from the owning process for template events, instance-created and the administrative-termination scope walk in the control plane, from the scheduler's frame engine at settlement, and from the supervisor at sub-graph and fan-out-partition rendezvous — and the kill request indeed does not fire instance-terminated, leaving it to a two-second poll loop over terminated instances. The enumeration is nonetheless incomplete: the delete-instance route, which the action registry itself names the instance-terminate action, fires instance-terminated synchronously from within the request whenever the poll loop has not yet consumed the idempotency row, so instance-terminated has a second, synchronous, request-bound delivery site that neither the four-site enumeration nor the invariant admits.
