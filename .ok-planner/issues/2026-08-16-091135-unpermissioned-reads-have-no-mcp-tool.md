---
issue: unpermissioned-reads-have-no-mcp-tool
kind: audit
category: conflicting
artifacts:
  - story:mcp-transport
  - concept:control-api
status: verified
opened: 2026-08-16T09:11:35Z
---

# Three unpermissioned control-API operations have no MCP tool

The MCP transport is promised as a complete skin over the control API: an agent can perform every read and mutation rimsky offers without a second client. The catalog is computed from the action registry and covers all 44 permissioned actions with parity fully measured — but the registry's three unpermissioned actions (the liveness probe, the identity echo that answers "which key am I holding", and the CA-root fetch) name no tool, so an agent on MCP alone cannot ask whether the deployment is up or who it is without standing up a plain HTTP client. The audit flipped the MCP story to unsupported on that gap; the control-API concept's "same operation set across skins" carries the same three exceptions. The ruling decides whether the tools are added, the promise is qualified, or the three ride another MCP mechanism.

One constraint bears on the fix: the concept deliberately keeps the identity echo ungrantable (a caller cannot discover which key it holds in order to ask for the grant), so its tool cannot gate behind an ordinary permission — it needs the same posture the route has, not a copy of the tool pattern.

## Options

- Give each of the three registry entries a tool, with the identity echo and probe carrying their route's unpermissioned posture; cost: a variant of the tool pattern for postured actions.
- Qualify the story and the concept to the permissioned surface and say where liveness and identity live instead; cost: narrows the promise rather than closing the gap.
- Serve the three through MCP's own handshake or as resources; cost: a third exposure mechanism beside tools and routes.

The ruling decides whether the MCP skin becomes complete or the promise shrinks to what it covers.

## Ruling

> Recommended ruling (/verify-issues): Add tools for the three, carrying each route's posture (probe and CA-root unauthenticated, identity echo token-only) so the catalog matches the registry entry for entry, and leave the story and concept as written.
>
> Rationale: the catalog is registry-derived by design, so the fix is three registry entries naming a tool plus one posture-aware branch — smaller than a new mechanism and truer to "the same operation set" than narrowing the promise. Flip case: if MCP clients are expected to learn liveness and identity from the transport handshake, the third option is the cleaner home and the story should say so.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
