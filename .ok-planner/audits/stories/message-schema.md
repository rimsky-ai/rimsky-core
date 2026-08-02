---
audit: message-schema
artifact: story:message-schema
determination: supported
commit: b767a27d
audited: 2026-08-02T09:28:32Z
---

# Template authors declare accepted message types; unknown types fail loud

Supported. Templates declare a `messages` registry (`lib/foundation/spec` / `lib/graph/node` `MessageSchema`), validated at registration by `ValidateTemplate` (checked by `TestValidateMessages_Ok_DeclaredTypeAndBodySchema` and related cases in `template_validator_messages_test.go`). At receipt, `handleCreateMessage` builds the declared-type set from the template spec plus the implicit empty type and refuses any other type with HTTP 400 naming the type and the declared set (`unknownMessageTypeError`), never a silent dead-letter — exercised by `TestCreateMessage_DeclaredTypeAccepted`, `TestCreateMessage_UndeclaredTypeRefused`, and the end-to-end `TestStoryMessageSchema_DeclaredAndUndeclaredTypes` in `test/scenarios/story_message_schema_e2e_test.go`.
