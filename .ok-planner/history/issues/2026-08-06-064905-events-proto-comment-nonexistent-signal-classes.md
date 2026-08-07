---
issue: events-proto-comment-nonexistent-signal-classes
kind: audit
category: doc-drift
artifacts:
  - concept:signal
status: answered
opened: 2026-08-06T06:49:05Z
---

# Does `events.proto`'s `OperationalKind` comment still advertise nonexistent `event/...` / `message/...` signal classes?

No — the filed gap no longer exists. The working tree now strips every
prose comment from `lib/protocols/proto/v1/*.proto` (only license headers
and `@concept:`/`@story:`/`@decision:` citation lines remain), enforced by
the pin test `test/plumbline/proto_comment_hygiene_test.go::TestProtoSourcesCarryNoProseComments`
(confirmed passing on the current tree). `events.proto`'s `OperationalKind`
enum now carries no doc comment at all, so it cannot advertise the
nonexistent `event/...` / `message/...` taxonomy the issue described.

Deciding artifact: `decision:comment-hygiene-uniform-rule` — proto sources
carry only license headers and citation tags; load-bearing prose belongs in
the design corpus, a name, or a test. The signal taxonomy itself is
correctly described in `concept:signal` (three kinds: `terminal`,
`transient`, `attribute`).
