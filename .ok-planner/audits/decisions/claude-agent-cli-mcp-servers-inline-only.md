---
audit: claude-agent-cli-mcp-servers-inline-only
artifact: decision:claude-agent-cli-mcp-servers-inline-only
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:43:41Z
---

# Inline-only per-node MCP declarations across three transports with one alias

Supported. The node-config schema models the MCP server list as an array of inline objects, requires a transport discriminator and a name on every entry, and branches into exactly three shapes: one requiring a URL, one requiring a command, and one requiring a module specifier and accepting either the module transport or its alias. The handler mirrors that: it switches on the transport, rejects an entry whose transport-appropriate field is empty with a message naming the entry, resolves the module transport and its alias down the same loopback path, and rejects any other transport value — including an absent one — as unknown. There is no reference-by-name field on the entry type, the entry type carries no field the three inline shapes do not use, and nothing in the executor reads an operator-side MCP catalog from configuration or environment at startup. A unit test drives all three transports through to the spawn request in one dispatch and checks the resulting tool configs, and separate tests cover the alias spelling through the schema, the request parser, and the server surface, plus an end-to-end scenario that calls a live module-loopback server's own tool from the CLI child.
