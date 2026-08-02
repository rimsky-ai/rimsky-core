---
audit: fanout-any-substitution-source
artifact: story:fanout-any-substitution-source
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:34:15Z
---

# Fan-out `partition_request` resolves uniformly from any standard substitution source

Supported. `substituteFanOutPartitionRequest` (`lib/runtime/runner_acquire_helpers.go`) builds the same `attributes.ResolveContext` used for ordinary attribute and locks-stage substitution and calls the single generic `attributes.SubstituteValue` — there is no partition-request-specific resolution path, so whatever source kinds that engine supports it supports here by construction. Of the four sources the story names (upstream node attribute, claim payload, instance param, typed message), three have dedicated unit tests driving `substituteFanOutPartitionRequest` itself with real dependency wiring (`TestSubstituteFanOutPartitionRequest_BindsFromNodeAttribute`, `_BindsFromAcquiredClaim`, `_BindsFromHeldClaim`, `_OverrideBindsFromMessage` in `lib/runtime/runner_acquire_helpers_test.go`); the fourth (instance params) flows through the identical `resolveCtx.Params` field exercised by the generic substitution suite and is confirmed reaching this call site by `TestSubstituteFanOutPartitionRequest_UnmarshalableInstanceParamsErrors`. A separate end-to-end example (`examples/fanout-any-source/`) exercises the node-attribute and message sources side by side against a live stack.
