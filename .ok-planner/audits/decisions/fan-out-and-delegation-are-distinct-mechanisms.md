---
audit: fan-out-and-delegation-are-distinct-mechanisms
artifact: decision:fan-out-and-delegation-are-distinct-mechanisms
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:10:48Z
---

# Fan-out and sub-graph delegation are two mechanisms sharing one thin dispatch helper

Supported, clause by clause. The two call sites of the shared dispatch helper — the only two that exist — pass structurally different matrices exactly as claimed: fan-out passes N partitions derived from its sub-claims against a single child whose node identity and executor are the calling node's own, while delegation passes one unkeyed partition against the sub-graph's internal nodes resolved from the template. Settlement is genuinely split into two primitives with almost no shared logic: one copies the exit's writeback onto the calling node and closes the child scope, the other records outcomes on the parent claim-handle, waits for every holder and every child handle to leave the active state, closes all partition scopes together, and resolves the parent claim under the aggregation policy. No shape discriminator or unified settle exists. The clone-identity claim is proved end to end by a scenario asserting one node row and one distinct node identifier across all three partition runs, with the parent's static-default attribute reaching every clone identically. The no-attribute-aggregation claim holds structurally: the fan-out settle merges no child attribute bag onto the parent, and the only per-partition value it does record is the producer's commit metadata under its own namespaced key, which is the producer-protocol route the decision points authors to. Composition is exercised by a scenario in which the fan-out node also declares a delegate, each partition running its own sub-graph and its sub-claim committing when that partition's exit carries back.
