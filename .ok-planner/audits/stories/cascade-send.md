---
audit: cascade-send
artifact: story:cascade-send
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:28:29Z
---

# Template author declares a message-sender node-type

Supported. The template DSL carries a `sends_message` field on a node definition (`lib/foundation/spec/template.go`), validated by `lib/graph/node/template_validator_messages.go::validateSendsMessage` (rejects unknown message types, whitespace-only values) and by `lib/graph/node/template_validator.go` (mutually exclusive with `executor`/`delegate`; the builtin send-message executor alias may only be reached via this field, never named directly). At dispatch, `lib/runtime/runner_dispatch.go` reads `NodeDef.SendsMessage` and wires the send callback that `lib/runtime/runner_send_message.go::sendCascadeMessage` and the builtin `lib/runtime/executor/builtin/send_message/handler.go` use to land an envelope in the message ledger. Four end-to-end tests carry the story's citation (`lib/services/test/scenarios/cascade_send_demo_e2e_test.go`, `test/scenarios/story_cascade_send_pipeline_e2e_test.go`, `test/scenarios/story_cascade_send_e2e_test.go` with two sub-cases covering a cycle and a loop shape) plus a shell-driven example (`examples/cascade-send-demo.sh`), all deploying a template with a `sends_message` node and asserting the resulting message lands in the ledger with instance-origin sender attribution and opens a new frame.
