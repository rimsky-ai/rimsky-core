---
audit: node-config-schema-format-go
artifact: decision:node-config-schema-format-go
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:29:05Z
---

# claude-agent embeds a single JSON-Schema document and validates writebacks with rimsky's own JSON-Schema library

Supported. `lib/services/executors/claude-agent/schema.go` embeds `expected_attributes_schema.json` via `//go:embed` and exposes it through `SchemaBytes()`; `observability.go`'s `CapabilitiesPayload` sets `ExpectedAttributesSchema: SchemaBytes()`, so the byte-identical embedded document is what the capability-advertisement handshake serves — there is no second, Go-struct-derived representation. `agentrun.go` imports `github.com/santhosh-tekuri/jsonschema/v5`, compiles the schema, and calls `validateAttributes.Validate(merged)` against the dispatch-time attribute writeback. The same library (`santhosh-tekuri/jsonschema/v5`, pinned identically in both the root and `lib/services` go.mod files) is the one rimsky's own core validation uses for attribute and template schemas (`lib/graph/attribute/validate.go`, `lib/graph/node/template_validator_attrschema.go`), matching the decision's "same JSON-Schema library rimsky itself uses" clause exactly.
