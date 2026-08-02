---
audit: claude-agent-cli-mcp-servers-inline-only
artifact: decision:claude-agent-cli-mcp-servers-inline-only
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:33:41Z
---

# Inline-only MCP server declarations across exactly three transports, http-loopback as a module alias

Supported. `mcptransport.go` declares exactly four transport strings — `http`, `stdio`, `module`, `http-loopback` — checked against both switch sites that resolve them (`requestparse.go::parseMcpServers`, `agentrun.go::resolveHostServers`); both switches route `module` and `http-loopback` through the identical module-loopback case, and any other value hits `unknownMcpTransportError`. The retired named-reference `{ref}` shape is explicitly detected and rejected with a message pointing at inline declaration, unit-tested in both the schema layer (`schema_test.go`) and the server-side parse layer (`server_test.go`). All three transports reaching a live spawn is proven by `TestRunAgentMcpServersReachSpawnAcrossTransports`, which configures one server of each transport in one dispatch and confirms each lands in the CLI child's tool config, including the module transport resolving to a real loopback server whose own tool the CLI successfully calls. No operator-side catalog or named-reference lookup exists anywhere in the package.
