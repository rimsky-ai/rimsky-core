---
issue: closed-value-sets-typed-bare-string
kind: human
category: design-convention
artifacts:
  - concept:cascade-mode
  - concept:claim
  - concept:publisher
status: answered
opened: 2026-08-06T08:07:36Z
github: https://github.com/rimsky-ai/rimsky-core/issues/59
---

# Do any of the four cited template fields still expose a closed vocabulary as a bare `string`, invisible to a godoc-driven reference generator?

No — the gap the issue names no longer exists, and the other two
citations do not describe a closed vocabulary to begin with.

Two of the four fields have since been converted from bare `string`
to a named type with a grouped const block, closing the completeness
gap exactly as the issue's candidate fix proposed:

- `TemplateNodeDef.CascadeMode` is now `spec.CascadeMode`
  (`lib/foundation/spec/enums.go`), a named `string` type with a
  four-member const block (`CascadeModeMostRecent`,
  `CascadeModeSequenced`, `CascadeModeIdempotentQueue`,
  `CascadeModeIdempotentSettled`) — at v0.13.0 (when this issue was
  filed) it was still bare `string`.
- `NodeClaimProducerRef.Intent` is now `claimproducer.Intent`
  (`lib/protocols/claimproducer/types.go`), a named `string` type with
  a grouped const block (`IntentRead`, `IntentReadWrite`) — also bare
  `string` at v0.13.0.

The other two citations were never closed vocabularies enforced in
code, so a named-type refactor does not apply to them: node `kind:`
(`TemplateNodeDef.Kind`) is validated against a deployment-registered
`KindAliasMap` (`lib/graph/node/kind_resolver.go`) — an open,
dynamically-extensible registry key, the same shape as `Executor
string`, not a fixed compile-time enum. `PublisherSpec.Kind` is
checked only for non-emptiness at registration
(`lib/graph/node/template_validator_messages.go::validatePublishers`)
— no fixed set is enforced anywhere in the tree. Both were bare
`string` with the identical, ungated shape at v0.13.0 too, so this
is not something that changed; re-reading the two Boundaries sections
(`concept:cascade-mode`, `concept:claim`) confirms neither commits to
a fixed enumeration for these two fields.
