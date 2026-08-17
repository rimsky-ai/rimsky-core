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

Sending a message over HTTP requires an idempotency key header. The decision requires it universally, because the caller that omits a key is the caller that retries and double-sends. The decision rejects an optional key, and it rejects content-hash dedup, because content-hash dedup reads two legitimate identical sends as a replay. The MCP tool fronts the same endpoint, declares the key optional, and mints a fresh random key per call when the caller omits one. An MCP client is an LLM agent, the least reliable retrier. A client that retries without choosing a key sends twice. The endpoint's own contract is intact. The MCP tool reintroduces the rejected shape one layer up. Content-hash keys stay ruled out. The ruling decides whether the tool requires the key or the owner accepts the exception.

## Options

- Require the key on the MCP tool; cost: model-driven callers must track a stable key.
- Accept the synthesized key as a documented MCP exception; cost: a live double-send exposure for the callers most likely to retry blindly.

The ruling decides whether the idempotency guarantee reaches MCP callers.

## Ruling

> Recommended ruling (/verify-issues): Require the key on the tool. The schema marks the key required, and an omitted key returns a tool error naming the field, so the guarantee is the same on every surface.
>
> Rationale: the decision argues from the retrying caller, and an agent is that caller. A required argument costs one line in a tool schema. The alternative is a double-send the audit reproduced. Flip case: if MCP clients cannot keep a key across a retry, name the tool non-idempotent. Ship a distinct "send-once" tool instead of minting a key silently.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
