---
audit: async-callback-persistent-registry
artifact: decision:async-callback-persistent-registry
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:36:14Z
---

# Whether the async-callback registration lives on the dispatch row and survives supervisor restart

Supported. The node-run ledger carries the acknowledgement id and its registration timestamp as columns in both drivers' initial migration, alongside the principal and callback URL added later; searching both migration sets for a callback-registry table finds none, so the rejected alternative genuinely was not taken. The dispatch path writes the registration and the await-async audit signal inside one transaction and fails the dispatch if that transaction fails. The callback handler resolves an arriving callback by looking the acknowledgement id up in its in-memory registry first and, on a miss, falling back to the persisted lookup keyed on that column — which the schema indexes for exactly this handler. An in-memory map still exists, but as a fast path over the persisted row rather than the sole record, which is what the rejected alternative was. Three independent tests span the restart claim: a scenario test that evicts the in-memory registration and then proves the callback still lands on the right run, and two protocol conformance-kit scenarios covering the async handoff and its survival across a restart.
