---
decision: test-harness-create-instance-wakes-roots-after-create
status: as-is
aliases: []
---

# Test-harness create-instance wakes roots after create

## Choice

The scenario test harness's create-instance helpers gain an internal empty-message wake step after the create POST so existing wait-for-root-dispatch semantics still hold without per-test changes.

## Rationale

Pre-spec, instance creation auto-fired the structural roots via the synthetic-envelope mechanism, which is what the harness's wait-for-root-dispatch waited for. Post-spec, instance creation is idle and a wake message is the only legitimate trigger. Folding the wake into the create-instance helper keeps the helper's existing contract intact and avoids per-test churn for a mechanism that's identical across every caller. Tests that need to observe the idle-on-create behavior use the control-api client directly, not the harness, so the helper's wake-after-create does not interfere.

## Alternatives considered

Split into create-idle-instance and create-and-wake-instance helpers and migrate every call site to pick — rejected: every existing caller wants the wake-after-create behavior, so the split adds two-name confusion with no payoff. Require per-test wake emission — rejected: identical cost-of-call across every test.
