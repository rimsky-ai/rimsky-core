---
audit: instance-lifecycle
artifact: story:instance-lifecycle
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:38:45Z
---

# An operator drives an instance's runtime existence end to end

Supported. All six ways the story names — create a live instance, watch its
progress, pause, resume, force-terminate, remove the record — were driven
through the public surface against a container of the released all-in-one
image. Creating an instance of a deployed template returned an id with its root
node materialized; after work was invoked the event log reported the node's
completion, the node listing reported it fresh and the status verb reported its
terminal success signal. On a second instance, pause reported and read back as
paused, work posted while paused stayed undelivered and nothing ran, and resume
released it so the held work completed. Terminate stamped the first instance
terminated and it read back terminated; the force kill verb did the same for
the second; after deleting both, neither appeared in the instance list. The
body states a role, a capability set and a mandatory benefit, names no surface
and no mechanism, and carries no history or forward-looking text.
