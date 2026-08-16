---
audit: node-config-schema-format-go
artifact: decision:node-config-schema-format-go
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:43:41Z
---

# The claude-agent's node-config schema is one embedded JSON document, validated by rimsky's library

Supported. The handler carries its node-config surface as a single JSON Schema document embedded into the binary at build time, and the accessor that hands it out returns a copy of those exact bytes. Advertisement is byte-identical: the capability message declares the field as raw bytes rather than a structured value, so the document travels through the observability capabilities response and through the bundled in-process advertisement path without any encode-decode round trip that could reshape it. Dispatch-time validation uses the node's declared schema — the one carried on the dispatch request, not the executor's own copy — compiled at dispatch start and applied to the writeback delta merged over the current attribute bag, so a violating writeback is rejected before it settles. The library is the same one rimsky itself uses: sweeping every JSON-Schema import in the tree gives five sites, four of them rimsky's own attribute, template, node-attribute-schema, and message-body validation, and the fifth the claude-agent executor, all on the same package at the same major version. The executor's schema test compiles the embedded document with that library and validates configurations against it.
