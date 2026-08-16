---
issue: mcp-bridge-makes-idempotency-key-optional
kind: audit
category: conflicting
artifacts:
  - decision:idempotency-key-header-universal
status: verified
opened: 2026-08-16T10:00:06Z
---

# The MCP message-send tool makes the idempotency key optional, which the header decision rejects

Sending a message over HTTP requires an idempotency key header — universally, because the caller that omits a key is the caller that retries and double-sends; the decision rejects an optional key and rejects content-hash dedup (two legitimate identical sends look like a replay). The MCP tool that fronts the same endpoint declares the key optional and mints a fresh random one per call when omitted; an MCP client (an LLM agent — the least reliable retrier) that retries without choosing a key sends twice. The endpoint's own contract is intact; the bridge reintroduces the rejected shape one layer up. Content-hash keys are already ruled out. The ruling decides whether the tool requires the key or the exception is accepted.

## Options

- Require the key on the MCP tool; cost: pushes stable-key tracking onto model-driven callers.
- Accept the synthesized key as a documented MCP exception; cost: a live double-send exposure for the population most likely to retry blindly.

The ruling decides whether the idempotency guarantee reaches MCP callers.

## Ruling

> Recommended ruling (/verify-issues): Require the key on the tool — the schema marks it required, an omitted key is a tool error naming the field — so the guarantee is the same on every skin.
>
> Rationale: the decision's whole argument is about the retrying caller, and an agent is exactly that caller; a required argument is one line in a tool schema, and the alternative is a double-send the audit already reproduced. Flip case: if MCP clients cannot be expected to keep a key across a retry, the honest move is to make the tool non-idempotent by name (a distinct "send-once" tool) rather than silently minting.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
