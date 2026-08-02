---
audit: empty-message-as-root-trigger
artifact: decision:empty-message-as-root-trigger
determination: supported
commit: b767a27d
audited: 2026-08-02T09:28:32Z
---

# The empty type-path is an implicit declared-types entry with a null body schema, refused if author-declared

Supported. Both the message-send handler (`lib/control/controlapi/messages.go`) and the instance-factory node materialization (`lib/control/controlapi/instances.go`) prepend `""` to the declared-types set built from `tpl.Spec.Messages`, seeding the implicit entry uniformly at registration/creation rather than through a dedicated endpoint or a receipt-handler special case. `TestValidateMessages_Error_EmptyType` in `lib/graph/node/template_validator_messages_test.go` confirms an author-declared empty-type entry is refused at registration as reserved-for-runtime. `TestFindMessageReceiverNode_EmptyTypeUnifiedWithUnmatchedType` and `TestDeliverNamedMessageInTx_EmptyTypeIsANoOpDeadLetterLikeAnyUnmatchedType` in `lib/runtime/message_delivery_empty_wake_test.go` explicitly assert that the receipt/delivery code path treats a missing empty-type receiver identically to a missing receiver for any other type, with no branch named for the empty case.
