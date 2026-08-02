---
audit: claude-agent
artifact: story:claude-agent
determination: supported
commit: b767a27d
audited: 2026-08-02T09:33:41Z
---

# Operator wires an agentic node with dispatch, per-node MCP/expose-env boundaries, sign-off, and error observability

Supported. The bundled `claude-agent` executor dispatches async agent work over the standard executor protocol (`RunAgent`/`agentrun.go`), and each of the story's four sub-capabilities is independently proven: per-node `cli.mcp_servers` and `cli.expose_env` intersected against operator env-var allowlists (`resolveHostServers`, `firstDisallowedExposeEnv` in `agentrun.go`), exercised end-to-end by a three-node divergence test showing each node sees only its own declared server/variable and that plaintext secret values never land in rimsky's persisted attribute bag; a cryptographic (Ed25519) sign-off gate that verifies a signature over the canonicalized value actually found at the declared path inside the dispatch's own `attributes_delta`, domain-separated by the node-run id (`signoff.go::VerifyRequiredSignoffs`/`BuildSignoffMessage`), with dedicated unit and ordering tests; and a closed, wildcard-capable declared error-class vocabulary (`schema.go::DeclaredErrorClasses`) advertised through the executor's observability capabilities and enforced by rejecting undeclared classes at the `report_error` boundary (unit-tested), which is exactly the vocabulary the platform's general-purpose error-policy routing and wildcard subscription targets range-check against. No sub-claim rests on a mechanism that exists only in the corpus text without a corresponding enforcement point and test.
