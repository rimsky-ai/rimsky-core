---
audit: all-upstream-gating
artifact: story:all-upstream-gating
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:39:31Z
---

# Fan-in receiver dispatches only after every in-flight upstream in the frame resolves

Supported: a run through the control API of an all-in-one deployment drove a
template whose fan-in receiver subscribes to two upstream siblings whose
staleness arrived by three different routes — a structural root woken by the
operator message, a sibling woken by cascade from a third node, and that same
sibling restaled a second time by operator invalidation. One upstream was held
in flight at a pause-mode pre-dispatch breakpoint while the other settled twice;
across both settlements the receiver never dispatched. Releasing the breakpoint
let the held upstream settle, after which the receiver dispatched exactly once,
after the last upstream completion in the event sequence, carrying values from
both upstreams. Seven checks, none failing.
