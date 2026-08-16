---
audit: three-dispatch-deadlines
artifact: decision:three-dispatch-deadlines
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:43:41Z
---

# Three independent dispatch deadlines, zero disabling, and no ceiling beneath the sync one

Supported. All three deadlines exist as node-template fields on the node definition, all three are validated as durations at template registration with a negative or unparseable value rejected naming the field, and all three read their default from the deployment configuration's dispatch block, which declares exactly those three keys and no fourth. Each has its own resolver — node value first, then the deployment default, then a built-in fallback — and the three are applied by different machinery to different questions: the synchronous one wraps the outbound dispatch call's context, and the quiet-period and runtime ones are carried onto the dispatch and swept by the conductor's deadline pass. Zero disables rather than defaults: the synchronous deadline is applied only when positive, and the other two are converted to a carried value only when positive, so a node declaring zero gets no bound. Nothing declares a deadline from the executor side — the executor advertisement carries only a schema, tags, and error classes, and no deadline resolver reads any advertised value. The sole-bound clause holds on the dispatch path: neither of rimsky's two remote executor clients nor its in-process client sets a client-level timeout, the bundled outbound-HTTP and verifier executors construct their outbound clients with none, and the host-agent proxy's executor path bounds only the spawn readiness handshake, not the forwarded call. A pin test asserts the outbound-HTTP executor's client timeout is zero on both its direct and its env-loaded construction paths, and then proves a blocked upstream call unblocks on the caller's context rather than any internal clock.
