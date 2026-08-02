---
audit: attribute-set-as-body
artifact: decision:attribute-set-as-body
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:33:46Z
---

# Send-node attribute schema must match the destination message body_schema exactly; the dispatched attribute bag is marshaled verbatim as the body

Supported. `validateSendsMessage` (`lib/graph/node/template_validator_messages.go`) rejects registration unless the send-node's attribute properties and the destination message type's `body_schema` properties are identical sets with matching types, and unless their `required` sets match exactly — no partial overlap or superset/subset is accepted. The built-in `send_message` executor handler (`lib/runtime/executor/builtin/send_message/handler.go`) takes the dispatched, already-resolved `req.GetAttributes()` and marshals it directly as the outgoing message body, with no intermediate mapping step. `test/scenarios/story_cascade_send_pipeline_e2e_test.go` exercises both ends: it asserts a superset attribute schema is rejected at registration, and that the send-node's body reflects a substituted upstream attribute value verbatim in the delivered message.
