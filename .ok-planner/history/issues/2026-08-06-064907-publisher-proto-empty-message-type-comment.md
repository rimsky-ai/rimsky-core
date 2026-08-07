---
issue: publisher-proto-empty-message-type-comment
kind: audit
category: doc-drift
artifacts: []
status: answered
opened: 2026-08-06T06:49:07Z
---

# Does `publisher.proto`'s `SubscribeRequest.message_type` comment still contradict the receipt-time gate's actual behavior?

No — the filed gap no longer exists. The working tree strips every prose
comment from `lib/protocols/proto/v1/*.proto` (only license headers and
`@concept:`/`@story:`/`@decision:` citation lines remain), enforced by the
pin test `test/plumbline/proto_comment_hygiene_test.go::TestProtoSourcesCarryNoProseComments`
(confirmed passing). `publisher.proto`'s `SubscribeRequest.message_type`
field now carries no doc comment, so it cannot claim empty values are
rejected. `lib/control/controlapi/messages.go`'s receipt-time gate still
seeds the declared-types set with `""` (per `decision:empty-message-as-root-trigger`
— every template's declared-types set carries an implicit empty-string
type-path entry), so empty-typed messages still pass receipt, unchanged and
now undocumented-in-proto rather than misdocumented.

Deciding artifact: `decision:comment-hygiene-uniform-rule` — proto sources
carry only license headers and citation tags; the actual gate behavior is
correctly documented in `concept:message` (invariant: "Every template's
declared-types set carries an implicit empty-type entry seeded at
registration, so empty-typed messages pass receipt under the same uniform
check").
