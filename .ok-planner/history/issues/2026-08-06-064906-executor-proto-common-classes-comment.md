---
issue: executor-proto-common-classes-comment
kind: audit
category: doc-drift
artifacts: []
status: answered
opened: 2026-08-06T06:49:06Z
---

# Does `executor.proto`'s `Error` message still advertise illustrative-but-undefined "common classes"?

No — the filed gap no longer exists. The working tree strips every prose
comment from `lib/protocols/proto/v1/*.proto` (only license headers and
`@concept:`/`@story:`/`@decision:` citation lines remain), enforced by the
pin test `test/plumbline/proto_comment_hygiene_test.go::TestProtoSourcesCarryNoProseComments`
(confirmed passing). The `Error` message (`executor.proto`) now carries no
doc comment, so it cannot list `executor_blocked` / `rate_limited` /
`transient_io` as illustrative common classes.

Deciding artifact: `decision:comment-hygiene-uniform-rule` — proto sources
carry only license headers and citation tags; a vocabulary illustration like
this belongs in the design corpus or a test, not a wire-file comment.
