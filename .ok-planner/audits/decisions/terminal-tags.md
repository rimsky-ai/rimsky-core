---
audit: terminal-tags
artifact: decision:terminal-tags
determination: supported
commit: b767a27d
audited: 2026-08-02T09:38:14Z
---

# Terminal tags replace named events

Supported. `lib/protocols/proto/v1/executor.proto` gives `Success`, `Error`, and `Park` each a `repeated string tags` field with documented set semantics, and field 1 of the (formerly NamedEvent-carrying) terminal message is marked `reserved` with the comment "was: repeated NamedEvent events"; a grep of the whole proto tree finds no `NamedEvent` message or streaming named-event variant remaining. `lib/runtime/runner_dispatch.go` dedups tags at decode via `shared.DedupStrings` for Success/Error/Park and validates each tag against the executor's declared vocabulary in `validateTags`, rejecting undeclared tags as `executor_protocol_violation`. That vocabulary is `declared_tags` (`lib/protocols/proto/v1/executor_observability.proto`), and `lib/graph/node/template_validator.go` + `subscription_tag_gate.go` validate subscription `when:` filters against it at template registration, matching the claim that `declared_tags` is what the registration gate validates against. A dedicated conformance scenario (`lib/protocols/conformance/executor/scenarios/tags_round_trip.go`) round-trips tags end-to-end.
