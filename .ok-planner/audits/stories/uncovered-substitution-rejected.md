---
audit: uncovered-substitution-rejected
artifact: story:uncovered-substitution-rejected
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T07:40:00Z
---

# Registration refuses a substitution ref that no subscription covers, and shows the entry that would

Supported. Against an all-in-one deployment driven through the control API,
registering a template that read an upstream node's attribute without
subscribing to it was refused with no template id, and the refusal named the
ref, the receiving node, the schema property the ref sits in, and the exact
subscription entry that would cover it; adding that entry made the same template
register. A template reading a typed message's field without subscribing to that
message was refused the same way with its own covering entry. The validate route
returned the identical finding before any registration was attempted, so the
author sees it at authoring time rather than as a runtime failure.
