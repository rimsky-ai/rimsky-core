---
audit: send-as-node-kind
artifact: decision:send-as-node-kind
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:38:14Z
---

# Cascade message-send is a dedicated node-kind, not a per-node-type emissions block

Supported. A node declares message-send via a `sends_message` field in place of an `executor` binding; `lib/graph/node/kind_resolver.go::CanonicalizeSendMessageSugar` resolves this sugar onto the builtin send-message executor alias only when no `executor` is already set, and template validation (`lib/graph/node/template_validator.go` gates, `test/plumbline/message_sender_vocabulary_test.go`) rejects a node that names the builtin alias directly as its `executor`, so the alias is reachable only through `sends_message`. The concept doc `concept:message-sender-node` confirms send-nodes reuse the standard subscription and attribute machinery for aggregation with no new validation/substitution primitive, and a repo-wide search finds no "emissions block" concept anywhere in the codebase (the only "emissions" hits are unrelated log-message text). Three end-to-end scenario tests (`story_cascade_send_e2e_test.go`, `story_cascade_send_pipeline_e2e_test.go`, `story_typed_message_substitution_e2e_test.go`) exercise send-nodes dispatching to the message ledger.
