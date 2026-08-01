---
decision: node-config-schema-format-go
status: as-is
aliases: []
---

# claude-agent carries its node-config schema as an embedded JSON Schema

## Choice

The Go claude-agent handler embeds the JSON Schema for its node-config surface as an embedded file and validates dispatch-time attribute writebacks against the node's declared schema with the same JSON-Schema library rimsky itself uses for attribute and template validation. The schema is a portable artifact — a single JSON document served byte-identically through capability advertisement.

## Rationale

Keeping the schema as one portable JSON document means the validated shape and the advertised shape cannot diverge; matching the JSON-Schema library rimsky already ships adds no new dependency surface.

## Alternatives

- Re-express the schema as Go structs with tag-driven validation — rejected: divergent representations risk drift between the validated shape and the advertised document.
- Adopt a different Go JSON-Schema library — rejected: matching rimsky's existing choice avoids duplicating a dependency for the same job.
