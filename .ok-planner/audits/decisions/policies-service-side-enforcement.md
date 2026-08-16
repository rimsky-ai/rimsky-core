---
audit: policies-service-side-enforcement
artifact: decision:policies-service-side-enforcement
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:43:41Z
---

# Operator allowlists are enforced inside the bundled service, invisible to rimsky

Supported. Every operator allowlist in the tree is read and enforced entirely within the service that owns it: the claude-agent executor checks its MCP server-name and exposable-variable-name allowlists inside its own dispatch path, and the outbound-HTTP executor and HTTP poll sensor check their egress allowlists inside their own connect path — four allowlists, none of them reachable from rimsky. Rejection takes the executor protocol's ordinary error route: the dispatch settles as an errored outcome whose message and structured payload both name the specific disallowed entry alongside the instance and the node, which two tests assert field by field. Rimsky's side is inert as claimed. Sweeping the whole tree for the allowlist variables finds them read only under the services module; the dispatch request message carries no policy field of any kind, and neither does any other protocol message — the single occurrence of the word policy across the protocol definitions names a fan-out partition policy, an unrelated mechanism. Rimsky does validate node configuration against the shape the executor advertises, but that is the executor's own schema document rather than policy content, and the allowlists never appear in it.
