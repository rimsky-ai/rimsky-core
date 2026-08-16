---
audit: envelope-type-discriminator
artifact: decision:envelope-type-discriminator
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:29:43Z
---

# One type-path field discriminates messages, with no second kind vocabulary beside it

Supported. The message-send request body, the message list and get projections, and the persisted message row all carry the type-path under a single field named `type`, and the persisted row has no companion discriminator column — its only other categorising column names the sender's category, not the message's. The publisher-subscription wire messages carry the counterpart under `message_type`, as claimed; the `kind` field those same messages carry names which publisher implementation is in play, a different axis, not a message vocabulary. The receiver side uses the same field name on its subscription entries, and the send-message node declares its outgoing type-path in one place, so sender, envelope and subscription speak one vocabulary. Registration and send-time checks reject an envelope whose type-path matches no declared subscription target, and tests cover both the accepted and the refused case.
