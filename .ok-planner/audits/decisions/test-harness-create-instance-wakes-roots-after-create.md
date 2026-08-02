---
audit: test-harness-create-instance-wakes-roots-after-create
artifact: decision:test-harness-create-instance-wakes-roots-after-create
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:36:46Z
---

# Create-instance harness helpers wake structural roots internally

Supported. Checked all 3 create-instance-style helpers across the two scenario harnesses: `test/support/scenario/harness.go`'s `CreateInstance`/`CreateInstanceWithOverrides` (one implementation) and `CreateInstanceWithServiceBindings`/`CreateInstanceWithServiceBindingsAndTarget` (one implementation), plus `lib/services/test/harness/rimsky.go`'s `CreateInstance`. Each posts an empty-typed message and waits for root dispatch immediately after instance creation (the scenario harness gates this on `templateHasStructuralRoot`; the services harness calls `EmptyWakeAfterCreate` unconditionally) rather than requiring the calling test to emit the wake itself. No create-instance helper in either harness package omits the wake step.
