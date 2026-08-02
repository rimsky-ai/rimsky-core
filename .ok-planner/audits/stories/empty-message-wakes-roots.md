---
audit: empty-message-wakes-roots
artifact: story:empty-message-wakes-roots
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:28:32Z
---

# An empty-string-typed message wakes every structural root through the same send path

Supported. Every template's declared-types set carries an implicit empty-type entry seeded at registration (`lib/control/controlapi/messages.go` and `lib/control/controlapi/instances.go` both prepend `""` to the declared set), a message-receiver-node is materialized for it at instance creation alongside every author-declared type, and structural roots subscribe to it via runtime-injected edges. The end-to-end scenario `TestStory_EmptyMessageWakesRoots` in `test/scenarios/empty_message_wake/empty_message_wakes_roots_e2e_test.go` posts a plain `POST /instances/{id}/messages` with `type=""` (no crafted envelope, no dedicated endpoint) against a template with two independent roots (`root1`, `root2`) and confirms exactly one frame opens with `triggering_message_id` pointing at the emitted empty message and both roots run — the same `/messages` route every other message uses.
