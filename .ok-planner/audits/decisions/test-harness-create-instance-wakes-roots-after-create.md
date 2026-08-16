---
audit: test-harness-create-instance-wakes-roots-after-create
artifact: decision:test-harness-create-instance-wakes-roots-after-create
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:43:41Z
---

# The scenario harnesses' create-instance helpers wake structural roots after creating

Supported. Both scenario harnesses in the tree do it. The core harness has four create-instance helpers; two are thin delegations to the other two, and each of those two, after decoding the new instance id, checks whether the template carries a structural root and — when it does — posts an empty message with a per-instance idempotency key and then blocks until a root dispatch appears, which is the wait-for-root-dispatch semantics the decision names. The services harness's create-instance helper posts the same empty wake unconditionally through a named helper of its own, whose idempotency key the caller prefixes. No test using either helper emits its own wake for the same purpose. The escape hatch holds too: the scenario that observes idle-on-create does not call the harness helper at all — it posts to the instances route through its own local function and then asserts the instance has no frames, no nodes dispatched, and no messages, so the helper's wake cannot interfere with it.
