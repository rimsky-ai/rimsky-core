---
audit: pre-v1-pure-removal-for-retired-surfaces
artifact: decision:pre-v1-pure-removal-for-retired-surfaces
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:44:16Z
---

# Retired DSL surfaces are deleted outright; rejection comes from the ordinary validator

Supported. The cross-cutting-subscription (`instance: true`) surface's
retirement is a concrete, checkable instance: migration
`016-drop-wait-set-subscription-scope.sql` deletes its rows and drops its
column/constraint, and a repo-wide grep of hand-written `.go` source found
zero remaining references to `subscription_scope` or
`cross_cutting_subscription` anywhere — no recognizer, no migration error
string naming the old shape. More generally, config and template loading go
through `configload.DecodeStrict`, which sets YAML `KnownFields(true)` so an
unrecognized field fails generically through the decoder rather than a
named detection rule, and message-type validation
(`lib/graph/node/template_validator_messages.go`,
`lib/control/controlapi/messages.go`) rejects an undeclared type with the
generic `"unknown message type %q (declared types: %v)"` error rather than
a message naming any specific retired type. A targeted grep for
retirement-specific error phrasing ("no longer supported", "is retired",
"has been removed") across `cmd/` and `lib/` returned no hits.
