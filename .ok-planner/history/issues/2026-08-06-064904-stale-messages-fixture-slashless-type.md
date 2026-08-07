---
issue: stale-messages-fixture-slashless-type
kind: audit
category: bug
artifacts: []
status: repaired
opened: 2026-08-06T06:49:04Z
---

# Does the `examples/messages-as-nodes/template-valid.yaml` fixture register cleanly against the current message-type validator?

It did not: the fixture declared `messages: [{type: foo}]`, and
`validateMessages` (`lib/graph/node/template_validator_messages.go`)
unconditionally rejects a message type without a `/` ("must be a
slash-bearing type-path ... so it cannot collide with a node-type") —
an unambiguous, deliberately-documented rule with no carve-out, applied
uniformly to every declared message type. There was exactly one
compliant fix: rename the fixture's type to a slash-bearing value,
never touch the rule. The slash-less type wasn't just latent — the
sibling `demo.sh` script actually registers this exact fixture and
asserts success, so the fixture was concretely broken, not
theoretically so.

Repaired by renaming the type from `foo` to `demo/foo` throughout
`examples/messages-as-nodes/template-valid.yaml`: the `messages:`
declaration, the `subscribes: - node: foo` reference (message types
are subscribed to like nodes under this story), the
`{{messages.foo.body}}` substitution directive, and the prose comments
describing both. `examples/messages-as-nodes/template-undeclared.yaml`
was untouched (unaffected — it deliberately references an undeclared
type for the negative-case half of the demo).

Verified with a throwaway test that YAML-decodes the fixture into
`spec.TemplateSpec` (via the same yaml-to-JSON path the CLI's
`yq | curl` flow effectively performs) and runs
`node.ValidateTemplate` against it: zero validation errors post-rename
(previously it failed on the message-type slash rule; the only
remaining failures before adding a kind-alias stub were the expected
"kind not registered" errors from not wiring a real executor registry,
unrelated to this fixture). `go build ./...` remains clean.
