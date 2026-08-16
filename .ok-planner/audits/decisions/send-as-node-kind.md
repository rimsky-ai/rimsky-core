---
audit: send-as-node-kind
artifact: decision:send-as-node-kind
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:33:36Z
---

# Cascade message send is a node kind, not a per-node-type emissions block

Supported. A node-type declares the send with a message-send directive on the node definition; template canonicalization rewrites that directive to the builtin send-message executor alias, and the validator rejects the directive alongside an executor or a delegate binding as mutually exclusive, rejects it alongside a kind declaration, rejects a message type the template does not declare, and rejects the builtin executor named directly without the directive. No emissions block exists anywhere in the template schema or the validators. The send node is an ordinary node row: it carries subscriptions and an attributes schema like any other, and the builtin handler composes the message body by marshalling the node's already-resolved attribute bag, so multi-sender aggregation and body substitution run entirely on the existing subscription and attribute machinery with no send-specific substitution path. Covered by unit tests over the canonicalizer and the validator's exclusivity rules and by end-to-end scenarios that drive a send node through a gated loop and through a pipeline that opens the next frame.
