---
decision: test-harness-create-instance-wakes-roots-after-create
status: as-is
aliases: []
---

# Test-harness create-instance wakes roots after create

## Choice

The scenario test harness's create-instance helpers include an internal empty-message wake step after instance creation, so the helpers' wait-for-root-dispatch semantics hold without per-test wake emission.

## Rationale

Instance creation is idle and a wake message is the only legitimate trigger for the structural roots. Every caller of the create-instance helpers wants wait-for-root-dispatch semantics, so folding the wake into the helper serves all of them without per-test churn for a mechanism identical across every caller. Tests that need to observe the idle-on-create behavior use the control-api client directly, not the harness, so the helper's wake-after-create does not interfere.

## Alternatives

- Split into create-idle-instance and create-and-wake-instance helpers and migrate every call site to pick — rejected: every existing caller wants the wake-after-create behavior, so the split adds two-name confusion with no payoff.
- Require per-test wake emission — rejected: identical cost-of-call across every test.
