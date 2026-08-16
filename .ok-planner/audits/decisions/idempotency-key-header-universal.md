---
audit: idempotency-key-header-universal
artifact: decision:idempotency-key-header-universal
text: compliant
implementation: unsupported
commit: d977250c
audited: 2026-08-16T05:29:43Z
---

# A mandatory idempotency key on message send

Unsupported, because the shipped MCP surface makes the key optional. The HTTP message-send endpoint does what the decision says: it reads the idempotency-key header, refuses the request outright when the header is absent or blank, and every in-tree caller of that endpoint — the CLI client, the publisher kit, the two test harnesses, and the CLI's own test double — supplies one. The endpoint is the single send path for all three sender kinds, so the universality over senders holds. What does not hold is replay safety by construction: the MCP tool bridge that fronts the same endpoint treats the key as an optional tool argument and, when the caller omits it, mints a fresh random key per call before setting the header. Its published tool schema leaves the argument out of the required list and tells callers they may omit it and have the server synthesize one. That is exactly the optional-key shape the decision records as rejected, and it fails the same way the rejection predicts: an MCP client that retries a send without having chosen a key double-sends, because each attempt carries a different key. Only the caller-supplied path is covered by a replay test; nothing exercises or prevents the synthesized-key retry.
