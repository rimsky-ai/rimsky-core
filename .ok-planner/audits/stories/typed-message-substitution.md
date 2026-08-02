---
audit: typed-message-substitution
artifact: story:typed-message-substitution
determination: supported
commit: b767a27d
audited: 2026-08-02T09:32:02Z
---

# Message bodies are read/composed through the same substitution grammar as node attributes, addressed by declared type name

Supported. `{{messages.<type>.<field>}}` and `{{nodes.<type>.attribute.<field>}}` resolve through the same `resolveSubstitutionValue` function keyed by declared type name against the frame's dependency map, and message-sending nodes compose their body through the same node-attribute schema/substitution mechanism every other node uses — there is no separate message-composition code path. Registration-time validation both requires the field to exist in the message's declared `body_schema` (rejecting a receiver-side typo) and requires an emitting node's attribute schema not to exceed the declared body fields (rejecting emitter-side extras); both are proven by a full HTTP-registration scenario test asserting HTTP 400 with the offending field named in the response. A third scenario test deploys a template, posts a typed message, and asserts the receiver's persisted attributes contain the value pulled from the message body via `{{messages.ping/recheck.reason}}`, proving runtime resolution end to end.
