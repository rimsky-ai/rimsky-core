---
issue: example-fixture-declares-rejected-message-type
kind: human
category: bug
artifacts:
  - concept:message-schema
status: answered
opened: 2026-08-06T08:07:11Z
github: https://github.com/rimsky-ai/rimsky-core/issues/46
---

# Does the shipped `messages-as-nodes` fixture still declare a message type the validator rejects?

No — the gap is closed. `examples/messages-as-nodes/template-valid.yaml:16` now declares
`- type: demo/foo`, a slash-bearing type-path, which satisfies the
slash-bearing requirement enforced by `validateMessages`
(`lib/graph/node/template_validator_messages.go#312-320`, "must be a
slash-bearing type-path"). The rename to `demo/foo` was already made in
a prior sprint (commit `a973a395`, "drain the ruled intake"). No other
message-type declarations in the fixture use a slash-less type.
