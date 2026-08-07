---
issue: lock-kind-proto-comment-wrong-values
kind: audit
category: doc-drift
artifacts: []
status: answered
opened: 2026-08-06T06:49:08Z
---

# Does the `lock_kind` proto comment still list wrong/misspelled values?

No — the filed gap no longer exists. The working tree strips every prose
comment from `lib/protocols/proto/v1/*.proto` (only license headers and
`@concept:`/`@story:`/`@decision:` citation lines remain), enforced by the
pin test `test/plumbline/proto_comment_hygiene_test.go::TestProtoSourcesCarryNoProseComments`
(confirmed passing). Every `lock_kind` field in `events.proto` (lines 177,
188, 198) now carries no doc comment, so it cannot list the wrong
three-value, misspelled set. `lib/foundation/persistence/claim_handles.go`
(`LockKindScope`) still defines exactly the two real values, `named` and
`claim_scope`.

Deciding artifact: `decision:comment-hygiene-uniform-rule` — proto sources
carry only license headers and citation tags; the real value set belongs to
the Go source (`LockKindScope` and its sibling constants) or the design
corpus, not a wire-file comment that can drift from it.
