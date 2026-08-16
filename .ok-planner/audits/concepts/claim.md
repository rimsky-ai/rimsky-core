---
audit: claim
artifact: concept:claim
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:21:03Z
---

# The claim as a protocol-layer noun: its open result, its intent, and its three extensions

Supported; all three invariants check out. A claim is declared in the template as a six-field reference — the producer name, the selector, the intent, and the three extension fields — of which only the opaque data blob is not rimsky-aware, and the open verb answers with either an acquired result carrying address, payload, claim scope and realized write semantics or an unavailable response, with the resolved scope bytes landing on the claim-handle row. Intent is genuinely single-purpose at runtime: the one place it changes behaviour is the coexistence predicate, evaluated from the candidate's intent, the conflicting holder's intent and the holder's realized write semantics; everywhere else it is carried rather than consulted — copied onto sub-claims, forwarded in the registration-time validation request, echoed into the executor's claim handle and into an event payload — and no producer-side branch reads it. The default conflict predicate is byte-equal comparison of the scope bytes, replaced by the producer's own overlap predicate exactly when the scopes-conflict capability is advertised, and the same predicate is consulted both at acquisition and when checking sibling sub-claim scopes for overlap. All three extensions exist as described: lifetime governs whether the handle survives holding-subgraph completion in a committed-durable state, sub-claim chains hang off the parent pointer with the parent resolving only after its children, and co-holdership gives each holding run a row in the per-claim holder ledger with resolution deferred until every one is non-active. The content-inertness invariant is pinned mechanically rather than by convention: a test parses the runtime package and fails if the set of functions that parse a claim's address, payload or scope bytes differs from the three enumerated sites, in either direction; the terminal forensics record stores a hash of the scope rather than the scope, and the breakpoint dispatch snapshot carries no claim content at all. The two producer-side prohibitions are checked against any endpoint by the shipped conformance battery.
