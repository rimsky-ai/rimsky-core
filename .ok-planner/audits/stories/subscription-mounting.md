---
audit: subscription-mounting
artifact: story:subscription-mounting
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:55:31Z
---

# An operator watches a publisher subscription go from mounting to active, and sees a failure the create hid

Supported. Driven through the public surface against a released-image stack
wired to the released cron sensor as a declared publisher, on a template with one
message type, one node reacting to it, and one publisher entry. Eight checks,
none failing, over the two subscriptions the run produced. The instance exposed
one subscription per declared publisher entry with its publisher name, kind and
message type; the operator saw it in the mounting state and then in the active
state and in no other state; and active meant what the story says it means — a
message attributed to that publisher arrived and the node the template wired to
its type ran. The second instance, whose publisher this deployment does not run,
was created successfully with an id while its subscription reported failed with
the reason that the publisher is not in the deployment's registry — the silent
create the story exists to catch, made visible on the subscription.
