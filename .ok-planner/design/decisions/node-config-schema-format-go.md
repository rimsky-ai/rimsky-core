---
decision: node-config-schema-format-go
status: as-is
aliases: []
---

# claude-agent carries its node-config schema as an embedded JSON Schema

## Choice

The Go claude-agent handler embeds the JSON Schema for its node-config surface as an embedded file and validates dispatch-time attribute writebacks against the node's declared schema with the same JSON-Schema library rimsky itself uses for attribute and template validation. The schema remains a portable artifact (the same JSON document the retired TypeScript implementation advertised, updated for the inline-only MCP shape and the expose-env field) and is served byte-identically through capability advertisement.

## Rationale

Preserves the schema as a portable artifact; matching the JSON-Schema library rimsky already ships adds no new dependency surface.

## Alternatives

(i) Re-express the schema as Go structs with tag-driven validation — rejected: divergent representations risk drift; the JSON-Schema embed keeps parity. (ii) Adopt a different Go JSON-Schema library — rejected: matching rimsky's existing choice avoids duplicating a dependency for the same job.
