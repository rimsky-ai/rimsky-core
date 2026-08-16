---
audit: kind-sugar-resolver
artifact: decision:kind-sugar-resolver
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:21:13Z
---

# Kind sugar resolves to a pre-registered in-process executor at registration

Supported on all five clauses. The node definition carries an optional kind field distinct from the required node-type field that subscriptions route on. The alias map is static and populated from the very same table of builtin entries that populates the in-process registry, so the two are registered side by side from one source, and the control API builds it once at startup. Registration order makes the guarantees real: the deploy handler validates first and returns a rejection before any rewriting, and only then canonicalises kind into executor. That ordering matters because the canonicaliser overwrites the executor field unconditionally, so the mutual-exclusion guarantee rests entirely on the validator running first — it does, and the validator rejects a node declaring both kind and executor, and separately rejects kind combined with delegation or with message sending. An unregistered kind is rejected with a not-registered error just as an unknown executor is, including the case where no alias map is configured at all, and a node declaring neither field returns early from both the validator check and the canonicaliser, leaving the ordinary resolution path untouched. Each of those five behaviours has its own test, alongside tests for the rewrite itself, its idempotence, and duplicate kind registration.
