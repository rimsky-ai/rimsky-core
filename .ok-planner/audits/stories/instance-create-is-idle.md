---
audit: instance-create-is-idle
artifact: story:instance-create-is-idle
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T05:20:00Z
---

# Creating an instance runs nothing until work is invoked on it

Supported. Against a zero-config all-in-one deployment, a created instance
materialized its node graph and did nothing else: all 3 run counters on every
node read zero, the instance's event log was empty, and no message was enqueued.
The negative was anchored to a sibling rather than to elapsed time — a second
instance of the same template was created, woken by an operator message, and
driven to completion, proving the scheduler was live — and the untouched
instance, re-read at that point, still had no events and zero run counters.
Posting a message to it then drove it to completion, so creating an instance and
invoking work on it are two operator actions the deployment drives
independently.
