---
audit: idempotency-status-code-distinction
artifact: decision:idempotency-status-code-distinction
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:29:43Z
---

# Fresh sends answer created, replays answer OK with the original message identifier

Supported. The message-send handler inserts-or-looks-up the idempotency row inside its transaction, records whether the insert was fresh, and on the way out answers with the created status when it was and the OK status when it was not; on the replay branch it substitutes the stored message identifier for the one it had minted, so the caller gets back the original. Nothing in the response body marks a replay — the distinction is carried entirely by the status code, which is what the decision chose over the rejected body-marker alternative. A test posts the same body twice under one key and asserts created then OK, and that the second call returns the first call's message identifier.
