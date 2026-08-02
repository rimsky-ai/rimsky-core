---
audit: substitution-grammar-fallback-routing
artifact: decision:substitution-grammar-fallback-routing
determination: supported
commit: b767a27d
audited: 2026-08-02T09:32:02Z
---

# Unresolved refs — whichever absence cause — route through one fallback/lenient grammar

Supported. `resolveDirectiveValue` in `lib/graph/attribute/substitution.go` applies literal-fallback (`| <literal>`) or lenient (`?`) handling uniformly to any `ErrMissingSource`, with no branching on why the source was missing. Tracing the two absence causes the decision names: a sender outside the receiver's subscribed set is simply never written into the deps map by `populateSubscribedSenderDeps` (the type isn't in `senderTypeSet`), and a subscribed sender with no fresh-settled run is skipped the same way when `GetMostRecentSettledRun` returns nil — both converge on the identical missing-key state in the `Deps` map, so both hit the same `ErrMissingSource` branch in `resolveSubstitutionValue` and the same fallback/lenient handling downstream. An end-to-end scenario test (`TestLenientMarkerRecoveryE2E`) confirms a `?`-marked ref over an absent source resolves to empty at dispatch for a lenient node while the same ref without `?` drives a strict node to `failed`.
